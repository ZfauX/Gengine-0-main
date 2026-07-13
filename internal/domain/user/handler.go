// internal/domain/user/handler.go
package user

import (
	"errors"
	"net/http"
	"strings"

	"gengine-0/internal/config"
	"gengine-0/internal/pkg/audit"
	apperrors "gengine-0/internal/pkg/errors"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/sanitize"
	"gengine-0/internal/pkg/storage"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	csrf "github.com/utrack/gin-csrf"
	"gorm.io/gorm"
)

// isHTTPS определяет, используется ли HTTPS для текущего запроса.
// Проверяет TLS-соединение и заголовок X-Forwarded-Proto (для прокси).
func isHTTPS(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}
	if proto := c.GetHeader("X-Forwarded-Proto"); proto == "https" {
		return true
	}
	if proto := c.GetHeader("X-Forwarded-Protocol"); strings.HasSuffix(proto, "s") {
		return true
	}
	if proto := c.GetHeader("X-Url-Scheme"); proto == "https" {
		return true
	}
	return false
}

// setSecureCookie устанавливает cookie с правильными флагами безопасности:
// HttpOnly=true (защита от XSS), Secure=true только для HTTPS, SameSite=Lax.
func setSecureCookie(c *gin.Context, name, value string, maxAge int, path string) {
	secure := isHTTPS(c)
	c.SetCookie(name, value, maxAge, path, "", secure, true)
}

// ---------- Входные структуры для валидации ----------

// UserIDRequest используется для валидации ID пользователя в URL.
type UserIDRequest struct {
	ID uint `uri:"id" binding:"required,gt=0"`
}

// OAuthProviderRequest используется для валидации провайдера в URL.
type OAuthProviderRequest struct {
	Provider string `uri:"provider" binding:"required,oneof=google github yandex"`
}

// VerifyEmailRequest используется для валидации токена в query.
type VerifyEmailRequest struct {
	Token string `form:"token" binding:"required"`
}

// RegisterInput – регистрация.
type RegisterInput struct {
	Email    string `form:"email" binding:"required,email"`
	Password string `form:"password" binding:"required,min=8,max=72"`
	Name     string `form:"name" binding:"required,min=2,max=50"`
}

// LoginInput – вход.
type LoginInput struct {
	Email    string `form:"email" binding:"required,email"`
	Password string `form:"password" binding:"required"`
}

// ForgotInput – восстановление пароля.
type ForgotInput struct {
	Email string `form:"email" binding:"required,email"`
}

// ResetInput – сброс пароля.
type ResetInput struct {
	Token    string `form:"token" binding:"required"`
	Password string `form:"password" binding:"required,min=8,max=72"`
}

// UpdateProfileInput – обновление профиля.
type UpdateProfileInput struct {
	Name  string `form:"name" binding:"required,min=2,max=50"`
	Email string `form:"email" binding:"required,email"`
}

// ChangePasswordInput – смена пароля.
type ChangePasswordInput struct {
	OldPassword string `form:"old_password" binding:"required"`
	NewPassword string `form:"new_password" binding:"required,min=8,max=72"`
}

// RefreshTokenInput – запрос на обновление access-токена.
type RefreshTokenInput struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// ---------- Обработчики ----------

// AuthHandler обрабатывает аутентификацию и регистрацию.
type AuthHandler struct {
	cfg                  *config.Config
	authSvc              *AuthService
	userService          *UserService
	passwordResetSvc     *PasswordResetService
	emailVerificationSvc *EmailVerificationService
	oauthSvc             *OAuthService
	auditSvc             *audit.Service
}

func NewAuthHandler(
	cfg *config.Config,
	authSvc *AuthService,
	userService *UserService,
	passwordResetSvc *PasswordResetService,
	emailVerificationSvc *EmailVerificationService,
	oauthSvc *OAuthService,
	auditSvc *audit.Service,
) *AuthHandler {
	return &AuthHandler{
		cfg:                  cfg,
		authSvc:              authSvc,
		userService:          userService,
		passwordResetSvc:     passwordResetSvc,
		emailVerificationSvc: emailVerificationSvc,
		oauthSvc:             oauthSvc,
		auditSvc:             auditSvc,
	}
}

// ShowLoginForm отображает страницу входа.
func (h *AuthHandler) ShowLoginForm(c *gin.Context) {
	render.Page(c, http.StatusOK, "auth-login.html", gin.H{
		"csrf": csrf.GetToken(c),
	})
}

// Login обрабатывает вход пользователя.
func (h *AuthHandler) Login(c *gin.Context) {
	var input LoginInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "auth-login.html", gin.H{
			"Error": "Некорректные данные: " + err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	token, err := h.authSvc.Login(c.Request.Context(), input.Email, input.Password)
	if err != nil {
		render.Page(c, http.StatusUnauthorized, "auth-login.html", gin.H{
			"Error": "Неверный email или пароль",
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	userID, _, parseErr := h.authSvc.ParseToken(token)
	if parseErr != nil {
		log.Error().Err(parseErr).Msg("Login: failed to parse token for audit")
	} else {
		h.auditSvc.Log(userID, "login", "user", userID, input.Email)
	}

	setSecureCookie(c, "jwt", token, int(h.cfg.JWT.AccessExpiry.Seconds()), "/")

	user, err := h.userService.GetByEmail(c.Request.Context(), input.Email)
	if err == nil {
		deviceID := c.GetHeader("X-Device-ID")
		refreshToken, err := h.authSvc.GenerateRefreshToken(c.Request.Context(), *user, deviceID)
		if err == nil {
			setSecureCookie(c, "refresh_token", refreshToken, int(h.cfg.JWT.RefreshExpiry.Seconds()), "/auth/refresh")
		} else {
			log.Error().Err(err).Msg("Login: failed to generate refresh token")
		}
	}

	c.Redirect(http.StatusFound, "/dashboard")
}

// RefreshToken обновляет access-токен по refresh-токену.
func (h *AuthHandler) RefreshToken(c *gin.Context) {
	refreshToken, err := c.Cookie("refresh_token")
	if err != nil || refreshToken == "" {
		var input RefreshTokenInput
		if bindErr := c.ShouldBindJSON(&input); bindErr != nil || input.RefreshToken == "" {
			appErr := apperrors.NewUnauthorizedError("refresh token required")
			c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
				"error": appErr.Message,
				"code":  appErr.Code,
			})
			return
		}
		refreshToken = input.RefreshToken
	}

	newAccessToken, err := h.authSvc.RefreshAccessToken(c.Request.Context(), refreshToken)
	if err != nil {
		appErr := apperrors.NewUnauthorizedError(err.Error())
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	setSecureCookie(c, "jwt", newAccessToken, int(h.cfg.JWT.AccessExpiry.Seconds()), "/")

	c.JSON(http.StatusOK, gin.H{
		"access_token": newAccessToken,
		"expires_in":   int(h.cfg.JWT.AccessExpiry.Seconds()),
	})
}

// Logout выполняет выход из системы (удаляет куки, но не отзывает refresh-токен).
func (h *AuthHandler) Logout(c *gin.Context) {
	setSecureCookie(c, "jwt", "", -1, "/")
	setSecureCookie(c, "refresh_token", "", -1, "/auth/refresh")
	c.Redirect(http.StatusFound, "/")
}

// LogoutAll выполняет выход со всех устройств (отзывает все refresh-токены пользователя).
func (h *AuthHandler) LogoutAll(c *gin.Context) {
	userID := c.GetUint("userID")
	if userID == 0 {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}
	if err := h.authSvc.RevokeAllUserTokens(c.Request.Context(), userID); err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("LogoutAll: failed to revoke tokens")
	}
	setSecureCookie(c, "jwt", "", -1, "/")
	setSecureCookie(c, "refresh_token", "", -1, "/auth/refresh")
	c.Redirect(http.StatusFound, "/")
}

// ShowRegisterForm отображает страницу регистрации.
func (h *AuthHandler) ShowRegisterForm(c *gin.Context) {
	render.Page(c, http.StatusOK, "auth-register.html", gin.H{
		"csrf": csrf.GetToken(c),
	})
}

// Register обрабатывает регистрацию нового пользователя.
func (h *AuthHandler) Register(c *gin.Context) {
	var input RegisterInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "auth-register.html", gin.H{
			"Error": "Некорректные данные: " + err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	cleanName := sanitize.StripHTML(input.Name)
	cleanEmail := sanitize.StripHTML(input.Email)

	user, err := h.authSvc.Register(c.Request.Context(), cleanEmail, input.Password, cleanName)
	if err != nil {
		render.Page(c, http.StatusConflict, "auth-register.html", gin.H{
			"Error": "Email уже зарегистрирован",
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	h.auditSvc.Log(user.ID, "register", "user", user.ID, cleanEmail)
	c.Redirect(http.StatusFound, "/auth/login")
}

// ShowForgotForm отображает форму восстановления пароля.
func (h *AuthHandler) ShowForgotForm(c *gin.Context) {
	render.Page(c, http.StatusOK, "auth-forgot.html", gin.H{
		"csrf": csrf.GetToken(c),
	})
}

// ForgotPassword отправляет ссылку для сброса пароля.
func (h *AuthHandler) ForgotPassword(c *gin.Context) {
	var input ForgotInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "auth-forgot.html", gin.H{
			"Error": "Некорректный email",
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	user, err := h.userService.GetByEmail(c.Request.Context(), input.Email)
	if err != nil {
		log.Debug().Str("email", input.Email).Msg("ForgotPassword: user not found")
	} else {
		if _, err := h.passwordResetSvc.GenerateToken(c.Request.Context(), *user); err != nil {
			log.Error().Err(err).Str("email", input.Email).Msg("ForgotPassword: failed to generate token")
		}
	}

	render.Page(c, http.StatusOK, "auth-forgot.html", gin.H{
		"Message": "Инструкции отправлены на почту",
		"csrf":    csrf.GetToken(c),
	})
}

// ShowResetForm отображает форму установки нового пароля.
func (h *AuthHandler) ShowResetForm(c *gin.Context) {
	token := sanitize.StripHTML(c.Query("token"))
	render.Page(c, http.StatusOK, "auth-reset.html", gin.H{
		"Token": token,
		"csrf":  csrf.GetToken(c),
	})
}

// ResetPassword устанавливает новый пароль по токену и отзывает все refresh-токены пользователя.
func (h *AuthHandler) ResetPassword(c *gin.Context) {
	var input ResetInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "auth-reset.html", gin.H{
			"Token": c.PostForm("token"),
			"Error": "Некорректные данные: " + err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	var userID uint
	token, err := h.passwordResetSvc.passResetRepo.GetToken(c.Request.Context(), input.Token)
	if err == nil {
		userID = token.UserID
	}

	if err := h.passwordResetSvc.ResetPassword(c.Request.Context(), input.Token, input.Password); err != nil {
		render.Page(c, http.StatusBadRequest, "auth-reset.html", gin.H{
			"Token": input.Token,
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	if userID != 0 {
		if err := h.authSvc.RevokeAllUserTokens(c.Request.Context(), userID); err != nil {
			log.Error().Err(err).Uint("user_id", userID).Msg("ResetPassword: failed to revoke refresh tokens")
		}
	}

	c.Redirect(http.StatusFound, "/auth/login")
}

// VerifyEmail подтверждает email пользователя.
func (h *AuthHandler) VerifyEmail(c *gin.Context) {
	var req VerifyEmailRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		render.Page(c, http.StatusBadRequest, "auth-verify_error.html", gin.H{
			"Error": "Неверный или отсутствующий токен",
		})
		return
	}

	if _, err := h.emailVerificationSvc.VerifyToken(c.Request.Context(), req.Token); err != nil {
		render.Page(c, http.StatusBadRequest, "auth-verify_error.html", gin.H{
			"Error": err.Error(),
		})
		return
	}
	render.Page(c, http.StatusOK, "auth-verify_success.html", gin.H{})
}

// OAuthLogin начинает процесс OAuth-авторизации.
func (h *AuthHandler) OAuthLogin(c *gin.Context) {
	var req OAuthProviderRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверный провайдер")
		return
	}

	url, state, err := h.oauthSvc.GetAuthURL(req.Provider)
	if err != nil {
		render.RenderError(c, http.StatusBadRequest, err.Error())
		return
	}

	session := sessions.Default(c)
	session.Set("oauth_state", state)
	if err := session.Save(); err != nil {
		log.Error().Err(err).Msg("OAuthLogin: failed to save session")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	c.Redirect(http.StatusFound, url)
}

// OAuthCallback обрабатывает обратный вызов OAuth-провайдера.
func (h *AuthHandler) OAuthCallback(c *gin.Context) {
	var req OAuthProviderRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.Page(c, http.StatusBadRequest, "auth-login.html", gin.H{
			"Error": "Неверный провайдер",
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	code := c.Query("code")
	state := c.Query("state")
	if code == "" {
		render.Page(c, http.StatusBadRequest, "auth-login.html", gin.H{
			"Error": "Отсутствует код авторизации",
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	session := sessions.Default(c)
	savedState := session.Get("oauth_state")
	if savedState == nil || savedState != state {
		log.Warn().Str("provider", req.Provider).Str("state", state).Msg("OAuthCallback: state mismatch")
		render.Page(c, http.StatusBadRequest, "auth-login.html", gin.H{
			"Error": "Ошибка авторизации: неверный параметр state",
			"csrf":  csrf.GetToken(c),
		})
		return
	}
	session.Delete("oauth_state")
	if err := session.Save(); err != nil {
		log.Error().Err(err).Msg("OAuthCallback: failed to clear session")
	}

	user, err := h.oauthSvc.Authenticate(c.Request.Context(), req.Provider, code, state)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			render.Page(c, http.StatusBadRequest, "auth-login.html", gin.H{
				"Error": "Пользователь не найден",
				"csrf":  csrf.GetToken(c),
			})
		} else {
			render.Page(c, http.StatusBadRequest, "auth-login.html", gin.H{
				"Error": "Ошибка входа через " + req.Provider + ": " + err.Error(),
				"csrf":  csrf.GetToken(c),
			})
		}
		return
	}

	token, err := h.authSvc.GenerateJWT(*user)
	if err != nil {
		render.Page(c, http.StatusInternalServerError, "auth-login.html", gin.H{
			"Error": "Внутренняя ошибка",
			"csrf":  csrf.GetToken(c),
		})
		return
	}
	setSecureCookie(c, "jwt", token, int(h.cfg.JWT.AccessExpiry.Seconds()), "/")

	deviceID := c.GetHeader("X-Device-ID")
	refreshToken, err := h.authSvc.GenerateRefreshToken(c.Request.Context(), *user, deviceID)
	if err == nil {
		setSecureCookie(c, "refresh_token", refreshToken, int(h.cfg.JWT.RefreshExpiry.Seconds()), "/auth/refresh")
	} else {
		log.Error().Err(err).Msg("OAuthCallback: failed to generate refresh token")
	}

	c.Redirect(http.StatusFound, "/dashboard")
}

// ---------- Профиль ----------

type ProfileHandler struct {
	db         *gorm.DB
	storage    storage.FileStorage
	authSvc    *AuthService
	profileSvc *ProfileService
	userSvc    *UserService
}

func NewProfileHandler(db *gorm.DB, st storage.FileStorage, authSvc *AuthService, profileSvc *ProfileService, userSvc *UserService) *ProfileHandler {
	return &ProfileHandler{
		db:         db,
		storage:    st,
		authSvc:    authSvc,
		profileSvc: profileSvc,
		userSvc:    userSvc,
	}
}

// Show отображает личную страницу профиля.
func (h *ProfileHandler) Show(c *gin.Context) {
	userID := c.GetUint("userID")
	var user User
	if err := h.db.Preload("Achievements").First(&user, userID).Error; err != nil {
		render.RenderErrorPage(c, http.StatusNotFound)
		return
	}
	render.Page(c, http.StatusOK, "profile-show.html", gin.H{
		"User":          user,
		"Achievements":  user.Achievements,
		"CurrentUserID": userID,
		"csrf":          csrf.GetToken(c),
	})
}

// PublicProfile отображает публичную страницу профиля.
func (h *ProfileHandler) PublicProfile(c *gin.Context) {
	var req UserIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID пользователя")
		return
	}

	userID := req.ID
	currentUserID := c.GetUint("userID")

	var user User
	if err := h.db.Preload("Achievements").First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			render.RenderErrorPage(c, http.StatusNotFound)
		} else {
			log.Error().Err(err).Uint("user_id", userID).Msg("PublicProfile: failed to get user")
			render.RenderErrorPage(c, http.StatusInternalServerError)
		}
		return
	}
	if user.ProfileVisibility == "hidden" {
		render.RenderError(c, http.StatusForbidden, "Профиль скрыт")
		return
	}

	// Статистика
	stats, err := h.profileSvc.GetPublicProfileStats(c.Request.Context(), userID)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("PublicProfile: failed to get stats")
		stats = &UserStats{GamesCreated: 0, GamesPlayed: 0, Wins: 0, Rating: 0}
	}

	// Проверка подписки
	isFollowing := false
	if currentUserID != 0 && currentUserID != userID {
		isFollowing, err = h.profileSvc.IsFollowing(c.Request.Context(), currentUserID, userID)
		if err != nil {
			log.Error().Err(err).Uint("user_id", userID).Msg("PublicProfile: failed to check follow")
		}
	}

	// Последние игры автора
	recentGames, err := h.profileSvc.GetRecentGames(c.Request.Context(), userID)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("PublicProfile: failed to get recent games")
		recentGames = []RecentGame{}
	}

	render.Page(c, http.StatusOK, "profile-public.html", gin.H{
		"ProfileUser":   user,
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
	})
}

// UploadAvatar загружает аватар пользователя.
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
	defer func() { _ = file.Close() }()

	log.Info().
		Uint("user_id", userID).
		Str("filename", header.Filename).
		Int64("size", header.Size).
		Str("content_type", header.Header.Get("Content-Type")).
		Msg("UploadAvatar: received file")

	if header.Size > 2*1024*1024 {
		appErr := apperrors.NewBadRequestError("Размер файла не должен превышать 2 МБ")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	allowedTypes := []string{"image/jpeg", "image/png", "image/webp"}
	contentType := header.Header.Get("Content-Type")
	allowed := false
	for _, t := range allowedTypes {
		if contentType == t {
			allowed = true
			break
		}
	}
	if !allowed {
		appErr := apperrors.NewBadRequestError("Допустимы только JPEG, PNG и WebP")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	webPath, err := h.storage.Save("uploads/avatars", file, header.Filename, userID, 2*1024*1024, allowedTypes)
	if err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("UploadAvatar: storage save failed")
		appErr := apperrors.NewInternalError(err)
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	log.Info().Uint("user_id", userID).Str("path", webPath).Msg("UploadAvatar: file saved")

	if err := h.userSvc.UpdateAvatarPath(c.Request.Context(), userID, webPath); err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("UploadAvatar: failed to update avatar_path")
		if delErr := h.storage.Delete(webPath); delErr != nil {
			log.Error().Err(delErr).Str("path", webPath).Msg("UploadAvatar: failed to delete uploaded file")
		}
		appErr := apperrors.NewInternalError(err)
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	log.Info().Uint("user_id", userID).Str("path", webPath).Msg("UploadAvatar: avatar updated successfully")
	c.Redirect(http.StatusFound, "/profile")
}

// UpdateProfile обновляет имя и email.
func (h *ProfileHandler) UpdateProfile(c *gin.Context) {
	userID := c.GetUint("userID")

	var input UpdateProfileInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "profile-show.html", gin.H{
			"Error": "Некорректные данные: " + err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	cleanName := sanitize.StripHTML(input.Name)
	cleanEmail := sanitize.StripHTML(input.Email)

	if err := h.profileSvc.UpdateProfile(c.Request.Context(), userID, cleanName, cleanEmail); err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("UpdateProfile: failed to update")
		render.Page(c, http.StatusInternalServerError, "profile-show.html", gin.H{
			"Error": "Ошибка обновления профиля",
			"csrf":  csrf.GetToken(c),
		})
		return
	}
	c.Redirect(http.StatusFound, "/profile")
}

// ChangePassword меняет пароль пользователя и отзывает все refresh-токены.
func (h *ProfileHandler) ChangePassword(c *gin.Context) {
	userID := c.GetUint("userID")

	var input ChangePasswordInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "profile-show.html", gin.H{
			"Error": "Некорректные данные: " + err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	if err := h.userSvc.ChangePassword(c.Request.Context(), userID, input.OldPassword, input.NewPassword); err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("ChangePassword: failed to update")
		render.Page(c, http.StatusBadRequest, "profile-show.html", gin.H{
			"Error": "Неверный текущий пароль",
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	if err := h.authSvc.RevokeAllUserTokens(c.Request.Context(), userID); err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("ChangePassword: failed to revoke refresh tokens")
	}

	setSecureCookie(c, "refresh_token", "", -1, "/auth/refresh")

	c.Redirect(http.StatusFound, "/profile")
}

// ---------- Достижения ----------

type AchievementHandler struct {
	db *gorm.DB
}

func NewAchievementHandler(db *gorm.DB) *AchievementHandler {
	return &AchievementHandler{db: db}
}

// List отображает все достижения пользователя.
func (h *AchievementHandler) List(c *gin.Context) {
	userID := c.GetUint("userID")
	var achievements []Achievement
	if err := h.db.Joins("JOIN user_achievements ON user_achievements.achievement_id = achievements.id").
		Where("user_achievements.user_id = ?", userID).
		Find(&achievements).Error; err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("AchievementHandler.List: failed to fetch achievements")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	render.Page(c, http.StatusOK, "achievements-list.html", gin.H{
		"Achievements":  achievements,
		"CurrentUserID": userID,
	})
}

// ---------- Дашборд ----------

type DashboardHandler struct {
	dashboardService *UserDashboardService
	db               *gorm.DB
}

func NewDashboardHandler(dashboardService *UserDashboardService, db *gorm.DB) *DashboardHandler {
	return &DashboardHandler{dashboardService: dashboardService, db: db}
}

// Index отображает личный кабинет.
func (h *DashboardHandler) Index(c *gin.Context) {
	userID := c.GetUint("userID")
	dash, err := h.dashboardService.GetDashboard(userID)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("DashboardHandler.Index: failed to get dashboard")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	isAdmin := middleware.IsAdmin(c)
	render.Page(c, http.StatusOK, "dashboard-index.html", gin.H{
		"Dashboard":     dash,
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
	})
}
