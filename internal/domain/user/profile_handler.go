// internal/domain/user/profile_handler.go
package user

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/pkg/email"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/sanitize"
	"gengine-0/internal/pkg/sessionstore"
	"gengine-0/internal/pkg/storage"
	"gengine-0/internal/pkg/validation"

	csrf "gengine-0/internal/pkg/csrf"
	apperrors "gengine-0/internal/pkg/errors"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type ProfileHandler struct {
	storage    storage.FileStorage
	authSvc    *AuthService
	profileSvc *ProfileService
	userSvc    *UserService
	cfg        *config.Config
}

func NewProfileHandler(st storage.FileStorage, authSvc *AuthService, profileSvc *ProfileService, userSvc *UserService, cfg *config.Config) *ProfileHandler {
	return &ProfileHandler{
		storage:    st,
		authSvc:    authSvc,
		profileSvc: profileSvc,
		userSvc:    userSvc,
		cfg:        cfg,
	}
}

// Show отображает страницу профиля пользователя.
// @Summary Личный профиль
// @Description Отображает страницу профиля текущего пользователя с возможностью редактирования
// @Tags profile
// @Produce html
// @Success 200 {string} html "Страница профиля"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 404 {object} map[string]interface{} "handler.user_not_found"
// @Router /profile [get]
// @Security JWT
func (h *ProfileHandler) Show(c *gin.Context) {
	userID := c.GetUint("userID")
	// Subscriptions прелоадим для корректного подсчёта заполненности профиля (C12).
	// Через репозиторий, а не *gorm.DB в хендлере (C1).
	user, err := h.userSvc.GetByIDWithAchievementsAndSubscriptions(c.Request.Context(), userID)
	if err != nil {
		render.RenderErrorPage(c, http.StatusNotFound)
		return
	}
	// Calculate profile completion percentage
	completion := calculateProfileCompletion(user)
	themeSettings, tsErr := h.profileSvc.GetThemeSettings(c.Request.Context(), userID)
	if tsErr != nil {
		log.Warn().Err(tsErr).Uint("user_id", userID).Msg("Show: failed to load theme settings, using defaults")
		themeSettings = DefaultThemeSettings()
	}
	notifyDays, ndErr := h.profileSvc.GetNotifyGameDays(c.Request.Context(), userID)
	if ndErr != nil {
		log.Warn().Err(ndErr).Uint("user_id", userID).Msg("Show: failed to load notify days, using 0")
		notifyDays = 0
	}

	// F-1 (pass 48): статистика (игры/победы/рейтинг) и последние игры — те же
	// данные, что на публичном профиле, теперь доступны и в личном кабинете.
	stats, statsErr := h.profileSvc.GetPublicProfileStats(c.Request.Context(), userID)
	if statsErr != nil {
		log.Warn().Err(statsErr).Uint("user_id", userID).Msg("Show: failed to load stats, using zero")
		stats = &UserStats{}
	}
	recentGames, rgErr := h.profileSvc.GetRecentGames(c.Request.Context(), userID)
	if rgErr != nil {
		log.Warn().Err(rgErr).Uint("user_id", userID).Msg("Show: failed to load recent games")
		recentGames = []RecentGame{}
	}

	render.Page(c, http.StatusOK, "profile-show.html", gin.H{
		"Title":          "Профиль",
		"User":           user.ToPublic(),
		"UserEmail":      user.Email,
		"Achievements":   user.Achievements,
		"VapidPublicKey": h.cfg.VAPID.PublicKey,
		"ThemeSettings":  themeSettings,
		"NotifyGameDays": notifyDays,
		"CurrentUserID":  userID,
		"ProfilePercent": completion,
		"Stats":          stats,
		"RecentGames":    recentGames,
		"csrf":           csrf.GetToken(c),
		"Breadcrumbs": []map[string]string{
			{"name": "nav.home", "url": "/"},
			{"name": "nav.profile"},
		},
	})
}

// calculateProfileCompletion вычисляет процент заполнения профиля (0-100).
func calculateProfileCompletion(u *User) int {
	if u == nil {
		return 0
	}
	total := 6
	completed := 0

	if u.Name != "" {
		completed++
	}
	if u.Email != "" {
		completed++
	}
	if u.AvatarPath != "" {
		completed++
	}
	if u.EmailVerified {
		completed++
	}
	if u.TwoFactorEnabled {
		completed++
	}
	if len(u.Subscriptions) > 0 {
		completed++
	}

	pct := (completed * 100) / total
	if pct > 100 {
		pct = 100
	}
	return pct
}

// PublicProfile отображает публичный профиль пользователя.
// @Summary Публичный профиль пользователя
// @Description Отображает публичный профиль пользователя по ID
// @Tags profile
// @Produce html
// @Param id path int true "ID пользователя"
// @Success 200 {string} html "Публичный профиль"
// @Failure 404 {object} map[string]interface{} "handler.user_not_found"
// @Router /users/{id} [get]
func (h *ProfileHandler) PublicProfile(c *gin.Context) {
	var req UserIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_user_id"))
		return
	}

	userID := req.ID
	currentUserID := c.GetUint("userID")

	user, err := h.userSvc.GetPublicProfile(c.Request.Context(), userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			render.RenderErrorPage(c, http.StatusNotFound)
		} else {
			log.Error().Err(err).Uint("user_id", userID).Msg("PublicProfile: failed to get user")
			render.RenderErrorPage(c, http.StatusInternalServerError)
		}
		return
	}
	// Скрытый профиль виден только владельцу (и админам через текущую проверку прав).
	if user.ProfileVisibility == "hidden" && currentUserID != userID {
		render.RenderError(c, http.StatusForbidden, "Профиль скрыт")
		return
	}

	stats, err := h.profileSvc.GetPublicProfileStats(c.Request.Context(), userID)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("PublicProfile: failed to get stats")
		stats = &UserStats{GamesCreated: 0, GamesPlayed: 0, Wins: 0, Rating: 0}
	}

	isFollowing := false
	if currentUserID != 0 && currentUserID != userID {
		isFollowing, err = h.profileSvc.IsFollowing(c.Request.Context(), currentUserID, userID)
		if err != nil {
			log.Error().Err(err).Uint("user_id", userID).Msg("PublicProfile: failed to check follow")
		}
	}

	recentGames, err := h.profileSvc.GetRecentGames(c.Request.Context(), userID)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("PublicProfile: failed to get recent games")
		recentGames = []RecentGame{}
	}

	pubUser := user.ToPublic()
	render.Page(c, http.StatusOK, "profile-public.html", gin.H{
		"Title":         "Профиль пользователя",
		"ProfileUser":   &pubUser,
		"ProfileEmail":  user.Email, // only shown in template if IsOwner
		"Achievements":  user.Achievements,
		"CurrentUserID": currentUserID,
		"IsOwner":       currentUserID == userID,
		"GamesCreated":  stats.GamesCreated,
		"GamesPlayed":   stats.GamesPlayed,
		"Wins":          stats.Wins,
		"Rating":        stats.Rating,
		"IsFollowing":   isFollowing,
		"RecentGames":   recentGames,
		"csrf":          csrf.GetToken(c),
		"Breadcrumbs": []map[string]string{
			{"name": "nav.home", "url": "/"},
			{"name": "nav.user_profile"},
		},
	})
}

// UploadAvatar загружает новый аватар.
// @Summary Загрузка аватара
// @Description Загружает новый аватар пользователя (изображение до 5 МБ)
// @Tags profile
// @Accept multipart/form-data
// @Produce html
// @Param avatar formData file true "Файл изображения"
// @Success 302 {string} string "Перенаправление на /profile"
// @Failure 400 {object} map[string]interface{} "Ошибка загрузки"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /profile/avatar [post]
// @Security JWT
func (h *ProfileHandler) UploadAvatar(c *gin.Context) {
	userID := c.GetUint("userID")
	if userID == 0 {
		log.Warn().Msg("UploadAvatar: user not authenticated")
		c.Redirect(http.StatusFound, "/profile")
		return
	}

	file, header, err := c.Request.FormFile("avatar")
	if err != nil {
		log.Warn().Err(err).Uint("user", userID).Msg("UploadAvatar: no file provided")
		c.Redirect(http.StatusFound, "/profile")
		return
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			log.Debug().Err(closeErr).Msg("profile: file close")
		}
	}()

	log.Info().
		Uint("user_id", userID).
		Str("filename", header.Filename).
		Int64("size", header.Size).
		Str("content_type", header.Header.Get("Content-Type")).
		Msg("UploadAvatar: received file")

	if header.Size > avatarMaxSize {
		appErr := apperrors.BadRequest("Размер файла не должен превышать 2 МБ")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	allowedTypes := []string{"image/jpeg", "image/png", "image/webp"}

	webPath, err := h.storage.Save("uploads/avatars", file, header.Filename, userID, avatarMaxSize, allowedTypes)
	if err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("UploadAvatar: storage save failed")
		appErr := apperrors.Wrap(err, "UploadAvatar: storage save failed")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	log.Info().Uint("user_id", userID).Str("path", webPath).Msg("UploadAvatar: file saved")

	oldPath, err := h.userSvc.UpdateAvatarPath(c.Request.Context(), userID, webPath)
	if err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("UploadAvatar: failed to update avatar_path")
		if delErr := h.storage.Delete(webPath); delErr != nil {
			log.Error().Err(delErr).Str("path", webPath).Msg("UploadAvatar: failed to delete uploaded file")
		}
		appErr := apperrors.Wrap(err, "UploadAvatar: failed to update avatar_path")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	// L12 (PASS-3): удаляем старый аватар (не оставляем мусор на диске).
	// Пропускаем, если пути нет (первый аватар) или совпадает с новым.
	if oldPath != "" && oldPath != webPath {
		if delErr := h.storage.Delete(oldPath); delErr != nil {
			log.Warn().Err(delErr).Str("path", oldPath).Uint("user_id", userID).Msg("UploadAvatar: failed to delete old avatar")
		}
	}

	log.Info().Uint("user_id", userID).Str("path", webPath).Msg("UploadAvatar: avatar updated successfully")
	c.Redirect(http.StatusFound, "/profile")
}

// UpdateProfile обновляет имя и email пользователя.
// @Summary Обновление профиля
// @Description Изменяет имя и email текущего пользователя
// @Tags profile
// @Accept x-www-form-urlencoded
// @Produce html
// @Param name formData string true "Новое имя"
// @Param email formData string true "Новый email"
// @Success 302 {string} string "Перенаправление на /profile"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /profile/update [post]
// @Security JWT
func (h *ProfileHandler) UpdateProfile(c *gin.Context) {
	userID := c.GetUint("userID")

	var input UpdateProfileInput
	errs := validation.FieldErrors{}
	if err := c.ShouldBind(&input); err != nil {
		errs.Add("name", validation.ValidateString("Имя", input.Name, 1, 128))
		errs.Add("email", validation.ValidateString("Email", input.Email, 1, 255))
		if !errs.HasErrors() {
			errs.Add("form", errors.New("некорректные данные"))
		}
		render.Page(c, http.StatusBadRequest, "profile-show.html", gin.H{
			"Title":  "Профиль",
			"Errors": errs,
			"Error":  errs.Error(),
			"csrf":   csrf.GetToken(c),
		})
		return
	}

	cleanName := sanitize.StripHTML(input.Name)
	cleanEmail := sanitize.StripHTML(input.Email)

	// Security HIGH (PASS-10): смена email требует текущий пароль — иначе
	// украденная сессия (XSS/утечка JWT) позволяет захватить аккаунт
	// (сменить email → сбросить пароль на новый адрес).
	curUser, getErr := h.userSvc.GetByID(c.Request.Context(), userID)
	if getErr != nil {
		log.Error().Err(getErr).Uint("user", userID).Msg("UpdateProfile: failed to load user")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	if !strings.EqualFold(curUser.Email, cleanEmail) {
		if input.CurrentPassword == "" || bcrypt.CompareHashAndPassword([]byte(curUser.Password), []byte(input.CurrentPassword)) != nil {
			errs.Add("email", errors.New("для смены email введите текущий пароль"))
			render.Page(c, http.StatusBadRequest, "profile-show.html", gin.H{
				"Title":  "Профиль",
				"Errors": errs,
				"Error":  errs.Error(),
				"csrf":   csrf.GetToken(c),
			})
			return
		}
	}

	if err := h.profileSvc.UpdateProfile(c.Request.Context(), userID, cleanName, cleanEmail); err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("UpdateProfile: failed to update")
		errs.Add("email", err)
		if !errs.HasErrors() {
			errs.Add("form", err)
		}
		status := http.StatusInternalServerError
		if errors.Is(err, ErrEmailTaken) {
			status = http.StatusBadRequest
		}
		render.Page(c, status, "profile-show.html", gin.H{
			"Title":  "Профиль",
			"Errors": errs,
			"Error":  errs.Error(),
			"csrf":   csrf.GetToken(c),
		})
		return
	}

	// UX-1 (PASS-13): при смене email уведомляем СТАРЫЙ адрес — владелец
	// предыдущего почтового ящика узнаёт о смене (защита от захвата/ошибки).
	if !strings.EqualFold(curUser.Email, cleanEmail) && curUser.Email != "" {
		subject := "Ваш email был изменён"
		body := "Здравствуйте!\n\nВаш email на Gengine-0 был изменён на: " + cleanEmail + ".\n" +
			"Если вы не меняли email — немедленно восстановите доступ через сброс пароля.\n\n" +
			"— Команда Gengine-0"
		if enqErr := email.Enqueue(curUser.Email, subject, body); enqErr != nil {
			log.Warn().Err(enqErr).Uint("user", userID).Msg("UpdateProfile: failed to notify old email")
		}
	}

	c.Redirect(http.StatusFound, "/profile")
}

// ChangePassword изменяет пароль пользователя.
// @Summary Смена пароля
// @Description Изменяет пароль текущего пользователя после проверки старого пароля
// @Tags profile
// @Accept x-www-form-urlencoded
// @Produce html
// @Param old_password formData string true "Старый пароль"
// @Param new_password formData string true "Новый пароль"
// @Success 302 {string} string "Перенаправление на /profile"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /profile/change-password [post]
// @Security JWT
func (h *ProfileHandler) ChangePassword(c *gin.Context) {
	userID := c.GetUint("userID")

	var input ChangePasswordInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "profile-show.html", gin.H{
			"Title": "Профиль",
			"Error": render.LocalizeError(c, err.Error()),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	if input.OldPassword == input.NewPassword {
		render.Page(c, http.StatusOK, "profile-show.html", gin.H{
			"Title": render.Tr(c, "nav.profile"),
			"Error": render.Tr(c, "profile.change_password_same"),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	if err := validation.ValidatePasswordStrength(input.NewPassword); err != nil {
		render.Page(c, http.StatusBadRequest, "profile-show.html", gin.H{
			"Title": "Профиль",
			"Error": render.LocalizeError(c, err.Error()),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	if err := h.userSvc.ChangePassword(c.Request.Context(), userID, input.OldPassword, input.NewPassword); err != nil {
		// L7 (PASS-9): неверный пароль/блокировка — пользовательская ошибка (400);
		// прочие (DB-сбой) — 500 с общим текстом (раньше всё маскировалось под
		// «Неверный текущий пароль», скрывая реальную причину от мониторинга).
		if errors.Is(err, ErrChangePasswordWrong) {
			render.Page(c, http.StatusBadRequest, "profile-show.html", gin.H{
				"Title": "Профиль",
				"Error": render.Tr(c, "profile.change_password_wrong"),
				"csrf":  csrf.GetToken(c),
			})
			return
		}
		log.Error().Err(err).Uint("user", userID).Msg("ChangePassword: internal failure")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	if err := h.authSvc.RevokeAllUserTokens(c.Request.Context(), userID); err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("ChangePassword: failed to revoke refresh tokens")
	}

	// Блэклистим текущий JWT и очищаем обе куки — принудительный повторный вход.
	if jwtCookie, jwtErr := c.Cookie("jwt"); jwtErr == nil && jwtCookie != "" {
		h.authSvc.RevokeJWT(c.Request.Context(), jwtCookie)
	}
	setSecureCookie(c, "jwt", "", -1, "/")
	setSecureCookie(c, "refresh_token", "", -1, "/")
	// M3 (PASS-13): смена пароля — серверная сессия тоже удаляется
	// (не остаётся pending/2FA-данных после принудительного повторного входа).
	sessionstore.DeleteGinSession(c)
	// H5 (PASS-15): смена пароля отзывает trusted-device cookie 2FA — иначе
	// украденный «запомнить устройство» cookie переживал смену пароля и давал
	// обход 2FA до 30 дней.
	clearTrustedDeviceCookie(c)

	c.Redirect(http.StatusFound, "/profile")
}

// UpdateThemeSettings сохраняет настройки автоматической смены темы.
// @Summary Настройки темы
// @Description Сохраняет параметры автосмены темы: включена ли, время начала/конца тёмной темы
// @Tags profile
// @Accept x-www-form-urlencoded
// @Produce html
// @Param auto_theme formData bool false "Включена ли автоматическая смена темы"
// @Param dark_from formData string false "Начало тёмной темы (HH:MM)"
// @Param dark_to formData string false "Конец тёмной темы (HH:MM)"
// @Success 302 {string} string "Перенаправление на /profile"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Router /profile/theme-settings [post]
// @Security JWT
func (h *ProfileHandler) UpdateThemeSettings(c *gin.Context) {
	userID := c.GetUint("userID")

	autoTheme := c.PostForm("auto_theme") == "on" || c.PostForm("auto_theme") == "true" || c.PostForm("auto_theme") == "1"
	darkFrom := strings.TrimSpace(c.PostForm("dark_from"))
	darkTo := strings.TrimSpace(c.PostForm("dark_to"))
	// UX-5 (PASS-16): theme_mode — system/time/dark/light.
	themeMode := strings.TrimSpace(c.PostForm("theme_mode"))
	if !validThemeMode(themeMode) {
		themeMode = ""
	}

	// В режимах "system"/"dark"/"light" временной диапазон не нужен.
	if !autoTheme || themeMode == "system" || themeMode == "dark" || themeMode == "light" {
		darkFrom, darkTo = "", ""
	}

	if autoTheme && (themeMode == "" || themeMode == "time") {
		if !validThemeTime(darkFrom) || !validThemeTime(darkTo) {
			render.Page(c, http.StatusBadRequest, "profile-show.html", gin.H{
				"Title": "Профиль",
				"Error": render.Tr(c, "profile.theme_time_error"),
				"csrf":  csrf.GetToken(c),
			})
			return
		}
	}

	ts := ThemeSettings{
		AutoTheme: autoTheme,
		DarkFrom:  darkFrom,
		DarkTo:    darkTo,
		ThemeMode: themeMode,
	}

	if err := h.profileSvc.SaveThemeSettings(c.Request.Context(), userID, ts); err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("UpdateThemeSettings: failed to save")
		render.Page(c, http.StatusInternalServerError, "profile-show.html", gin.H{
			"Title": "Профиль",
			"Error": render.Tr(c, "profile.theme_save_error"),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	// Сбрасываем кэш темы, чтобы изменения применились сразу (P5).
	middleware.InvalidateThemeCache(userID)

	render.SetFlash(c, "success", render.Tr(c, "profile.settings_saved"))
	c.Redirect(http.StatusFound, "/profile")
}

// UpdateNotifyGameDays сохраняет период уведомлений о предстоящих играх (D-1).
// @Summary Настройка уведомлений об играх
// @Tags profile
// @Accept x-www-form-urlencoded
// @Param notify_game_days formData int false "За сколько дней уведомлять (30/14/7/1/0)"
// @Success 302 {string} string "Перенаправление на /profile"
// @Router /profile/notify-game-days [post]
// @Security JWT
func (h *ProfileHandler) UpdateNotifyGameDays(c *gin.Context) {
	userID := c.GetUint("userID")
	days, _ := strconv.Atoi(c.PostForm("notify_game_days"))

	// DEEP-REVIEW MEDIUM #25 (pass 46): валидация диапазона.
	// Допустимы 0 (никогда) и значения из селекта (1/7/14/30); отрицательные
	// и завышенные значения отклоняем, чтобы не мусорить в БД.
	if days < 0 || days > 365 {
		render.SetFlash(c, "error", render.Tr(c, "profile.invalid_notify_days"))
		c.Redirect(http.StatusFound, "/profile")
		return
	}

	if err := h.profileSvc.SaveNotifyGameDays(c.Request.Context(), userID, days); err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("UpdateNotifyGameDays: failed to save")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	render.SetFlash(c, "success", render.Tr(c, "profile.settings_saved"))
	c.Redirect(http.StatusFound, "/profile")
}

// validThemeTime проверяет строку времени в формате "HH:MM".
func validThemeTime(s string) bool {
	t, err := time.Parse("15:04", s)
	return err == nil && t != (time.Time{})
}

// validThemeMode проверяет режим темы (UX-5): system/time/dark/light/пустая строка.
func validThemeMode(m string) bool {
	switch m {
	case "", "system", "time", "dark", "light":
		return true
	default:
		return false
	}
}
