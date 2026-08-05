// internal/domain/user/two_factor_handler.go
package user

import (
	"net/http"
	"strings"
	"time"

	"gengine-0/internal/pkg/i18n"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"

	csrf "gengine-0/internal/pkg/csrf"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// TwoFactorHandler обрабатывает HTTP-запросы для 2FA.
type TwoFactorHandler struct {
	twoFactorSvc    *TwoFactorService
	authService     *AuthService
	userRepo        UserRepository
	jwtAccessExpiry time.Duration
}

// NewTwoFactorHandler создаёт новый handler 2FA.
func NewTwoFactorHandler(twoFactorSvc *TwoFactorService, authService *AuthService, userRepo UserRepository, jwtAccessExpiry time.Duration) *TwoFactorHandler {
	if jwtAccessExpiry <= 0 {
		jwtAccessExpiry = 15 * time.Minute
	}
	return &TwoFactorHandler{
		twoFactorSvc:    twoFactorSvc,
		authService:     authService,
		userRepo:        userRepo,
		jwtAccessExpiry: jwtAccessExpiry,
	}
}

// VerifyForm отображает форму ввода TOTP-кода.
// @Summary Форма 2FA верификации
// @Tags 2fa
// @Produce html
// @Success 200 {string} html "Форма 2FA"
// @Router /auth/2fa/verify [get]
func (h *TwoFactorHandler) VerifyForm(c *gin.Context) {
	userID := c.GetUint("userID")
	render.Page(c, http.StatusOK, "admin-2fa-verify.html", gin.H{
		"Title":         "Двухфакторная аутентификация",
		"Message":       render.Tr(c, "handler.enter_totp_code"),
		"ReturnURL":     safeReturnURL(c.Query("return_url"), "/"),
		"CurrentUserID": userID,
		"IsAdmin":       middleware.IsAdmin(c),
		"csrf":          csrf.GetToken(c),
	})
}

// Verify обрабатывает ввод TOTP-кода и устанавливает флаг в сессии.
// @Summary Проверка TOTP-кода
// @Tags 2fa
// @Accept x-www-form-urlencoded
// @Produce html
// @Param code formData string true "TOTP-код"
// @Success 302 {string} string "Перенаправление"
// @Router /auth/2fa/verify [post]
func (h *TwoFactorHandler) Verify(c *gin.Context) {
	userID := c.GetUint("userID")
	if userID == 0 {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}

	code := c.PostForm("code")
	returnURL := safeReturnURL(c.DefaultPostForm("return_url", ""), "/")

	baseData := func() gin.H {
		return gin.H{
			"Title":         "Двухфакторная аутентификация",
			"Message":       render.Tr(c, "handler.enter_totp_code"),
			"ReturnURL":     returnURL,
			"CurrentUserID": userID,
			"IsAdmin":       middleware.IsAdmin(c),
			"csrf":          csrf.GetToken(c),
		}
	}

	if err := h.twoFactorSvc.Validate2FAInput(code); err != nil {
		data := baseData()
		data["Error"] = err.Error()
		render.Page(c, http.StatusOK, "admin-2fa-verify.html", data)
		return
	}

	user, err := h.userRepo.GetByID(c.Request.Context(), userID)
	if err != nil {
		render.RenderErrorPage(c, http.StatusNotFound)
		return
	}

	valid, err := h.twoFactorSvc.VerifyCode(user.TwoFactorSecret, code)
	if err != nil || !valid {
		data := baseData()
		data["Error"] = render.Tr(c, "handler.wrong_code_try_again")
		render.Page(c, http.StatusOK, "admin-2fa-verify.html", data)
		return
	}

	// Сохраняем флаг верификации в сессии (привязан к userID)
	session := sessions.Default(c)
	session.Set(session2FAKey(userID), true)
	if err := session.Save(); err != nil {
		log.Error().Err(err).Msg("Verify: failed to save session")
	}

	// Session fixation protection: перевыпускаем JWT с новым jti.
	// Cookie store не поддерживает session.Regenerate(), поэтому выдаём новый access-токен
	// — старые (перехваченные до 2FA) становятся невалидны через jti blacklist.
	if userObj, err := h.userRepo.GetByID(c.Request.Context(), userID); err == nil {
		if newToken, jwtErr := h.authService.GenerateJWT(*userObj); jwtErr == nil {
			setSecureCookie(c, "jwt", newToken, int(h.jwtAccessExpiry.Seconds()), "/")
		} else {
			log.Error().Err(jwtErr).Uint("user_id", userID).Msg("Verify: failed to regenerate JWT")
		}
	}

	c.Redirect(http.StatusFound, returnURL)
}

// BackupForm отображает форму ввода резервного кода.
// @Summary Форма резервного кода 2FA
// @Tags 2fa
// @Produce html
// @Success 200 {string} html "Форма резервного кода"
// @Router /auth/2fa/backup [get]
func (h *TwoFactorHandler) BackupForm(c *gin.Context) {
	userID := c.GetUint("userID")
	render.Page(c, http.StatusOK, "admin-2fa-backup.html", gin.H{
		"Title":         "Резервный код 2FA",
		"CurrentUserID": userID,
		"IsAdmin":       middleware.IsAdmin(c),
		"csrf":          csrf.GetToken(c),
	})
}

// BackupVerify обрабатывает ввод резервного кода.
// @Summary Проверка резервного кода
// @Tags 2fa
// @Accept x-www-form-urlencoded
// @Produce html
// @Param backup_code formData string true "Резервный код"
// @Success 302 {string} string "Перенаправление"
// @Router /auth/2fa/backup [post]
func (h *TwoFactorHandler) BackupVerify(c *gin.Context) {
	userID := c.GetUint("userID")
	if userID == 0 {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}

	baseData := func() gin.H {
		return gin.H{
			"Title":         "Резервный код 2FA",
			"CurrentUserID": userID,
			"IsAdmin":       middleware.IsAdmin(c),
			"csrf":          csrf.GetToken(c),
		}
	}

	backupCode := c.PostForm("backup_code")
	if backupCode == "" {
		data := baseData()
		data["Error"] = render.Tr(c, "handler.enter_backup_code")
		render.Page(c, http.StatusOK, "admin-2fa-backup.html", data)
		return
	}

	user, err := h.userRepo.GetByID(c.Request.Context(), userID)
	if err != nil {
		render.RenderErrorPage(c, http.StatusNotFound)
		return
	}

	// Verify and remove the used backup code
	remainingCodes, verr := h.twoFactorSvc.VerifyAndRemoveBackupCode(user.TwoFactorBackupCodes, backupCode)
	if verr != nil {
		data := baseData()
		data["Error"] = "Неверный резервный код"
		render.Page(c, http.StatusOK, "admin-2fa-backup.html", data)
		return
	}

	// Persist remaining backup codes
	if err := h.userRepo.Update(c.Request.Context(), user.ID, map[string]any{
		"two_factor_backup_codes": remainingCodes,
	}); err != nil {
		log.Error().Err(err).Uint("user_id", user.ID).Msg("BackupVerify: failed to update backup codes")
	}

	// Сохраняем флаг верификации в сессии (привязан к userID)
	session := sessions.Default(c)
	session.Set(session2FAKey(userID), true)
	if err := session.Save(); err != nil {
		log.Error().Err(err).Msg("BackupVerify: failed to save session")
	}

	c.Redirect(http.StatusFound, "/")
}

// EnableForm отображает форму включения 2FA.
// @Summary Форма включения 2FA
// @Tags 2fa
// @Produce html
// @Success 200 {string} html "Форма включения 2FA"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /user/2fa/enable [get]
// @Security JWT
func (h *TwoFactorHandler) EnableForm(c *gin.Context) {
	userID := c.GetUint("userID")
	if userID == 0 {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}

	user, err := h.userRepo.GetByID(c.Request.Context(), userID)
	if err != nil {
		render.RenderErrorPage(c, http.StatusNotFound)
		return
	}

	if user.TwoFactorEnabled {
		render.Page(c, http.StatusOK, "user-2fa-enabled.html", gin.H{
			"Title": render.Tr(c, "handler.2fa_already_enabled"),
			"User":  user.ToPublic(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	// Генерируем секрет и QR-код
	secret, err := h.twoFactorSvc.GenerateSecret()
	if err != nil {
		log.Error().Err(err).Msg("EnableForm: failed to generate 2FA secret")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	qrURL, qrErr := h.twoFactorSvc.GenerateQRCodeURL(secret, user.Email, "Gengine-0")
	if qrErr != nil {
		log.Error().Err(qrErr).Msg("EnableForm: failed to generate QR code URL")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	// Сохраняем секрет в сессии для подтверждения на следующем шаге
	sess := sessions.Default(c)
	sess.Set("2fa_pending_secret", secret)
	sess.Set("2fa_pending_secret_url", qrURL)
	if err := sess.Save(); err != nil {
		log.Error().Err(err).Msg("EnableForm: failed to save session")
	}

	render.Page(c, http.StatusOK, "user-2fa-enable.html", gin.H{
		"Title":         "Включить 2FA",
		"User":          user.ToPublic(),
		"Secret":        secret,
		"QRURL":         qrURL,
		"CurrentUserID": userID,
		"IsAdmin":       middleware.IsAdmin(c),
		"csrf":          csrf.GetToken(c),
	})
}

// Enable подтверждает включение 2FA.
// @Summary Подтверждение включения 2FA
// @Tags 2fa
// @Accept x-www-form-urlencoded
// @Produce html
// @Param code formData string true "Код подтверждения"
// @Success 302 {string} string "Перенаправление в профиль"
// @Failure 400 {object} map[string]interface{} render.Tr(c, "handler.invalid_code")
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /user/2fa/enable [post]
// @Security JWT
func (h *TwoFactorHandler) Enable(c *gin.Context) {
	userID := c.GetUint("userID")
	if userID == 0 {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}

	var input struct {
		Code string `form:"code" binding:"required"`
	}
	if err := c.ShouldBind(&input); err != nil {
		c.Redirect(http.StatusFound, "/user/2fa/enable")
		return
	}

	user, err := h.userRepo.GetByID(c.Request.Context(), userID)
	if err != nil {
		render.RenderErrorPage(c, http.StatusNotFound)
		return
	}

	// Получаем секрет из сессии (сгенерирован на шаге EnableForm)
	sess := sessions.Default(c)
	pendingSecret, ok := sess.Get("2fa_pending_secret").(string)
	if !ok || pendingSecret == "" {
		render.Page(c, http.StatusBadRequest, "user-2fa-enable.html", gin.H{
			"Title": render.Tr(c, "twofa.page_title"),
			"Error": "Сессия истекла. Начните настройку 2FA заново.",
			"User":  user.ToPublic(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	// Проверяем код против сохранённого секрета
	valid, err := h.twoFactorSvc.VerifyCode(pendingSecret, input.Code)
	if err != nil || !valid {
		render.Page(c, http.StatusOK, "user-2fa-enable.html", gin.H{
			"Title": render.Tr(c, "twofa.page_title"),
			"Error": render.Tr(c, "handler.wrong_code_try_again"),
			"User":  user.ToPublic(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	// Генерируем резервные коды
	backupCodes, err := h.twoFactorSvc.GenerateBackupCodes()
	if err != nil {
		render.Page(c, http.StatusOK, "user-2fa-enable.html", gin.H{
			"Title": render.Tr(c, "twofa.page_title"),
			"Error": render.LocalizeError(c, err.Error()),
			"User":  user.ToPublic(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	hashedCodes, err := h.twoFactorSvc.HashBackupCodes(backupCodes)
	if err != nil {
		render.Page(c, http.StatusOK, "user-2fa-enable.html", gin.H{
			"Title": render.Tr(c, "twofa.page_title"),
			"Error": render.LocalizeError(c, err.Error()),
			"User":  user.ToPublic(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	// Включаем 2FA с подтверждённым секретом
	user.TwoFactorEnabled = true
	user.TwoFactorSecret = pendingSecret
	user.TwoFactorBackupCodes = hashedCodes

	// Сохраняем пользователя
	if err := h.userRepo.Update(c.Request.Context(), user.ID, map[string]any{
		"two_factor_enabled":      true,
		"two_factor_secret":       pendingSecret,
		"two_factor_backup_codes": hashedCodes,
	}); err != nil {
		render.Page(c, http.StatusOK, "user-2fa-enable.html", gin.H{
			"Title": render.Tr(c, "twofa.page_title"),
			"Error": render.LocalizeError(c, err.Error()),
			"User":  user.ToPublic(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	// Очищаем сессию
	sess.Delete("2fa_pending_secret")
	sess.Delete("2fa_pending_secret_url")
	sess.Delete(session2FAKey(userID))
	if err := sess.Save(); err != nil {
		log.Error().Err(err).Msg("Enable: failed to clear session")
	}

	// Показываем страницу успеха с резервными кодами
	render.Page(c, http.StatusOK, "user-2fa-enabled.html", gin.H{
		"Title":       "2FA включена",
		"User":        user.ToPublic(),
		"BackupCodes": backupCodes,
		"csrf":        csrf.GetToken(c),
	})
}

// DisableForm отображает форму отключения 2FA.
// @Summary Форма отключения 2FA
// @Tags 2fa
// @Produce html
// @Success 200 {string} html "Форма отключения 2FA"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /user/2fa/disable [get]
// @Security JWT
func (h *TwoFactorHandler) DisableForm(c *gin.Context) {
	userID := c.GetUint("userID")
	if userID == 0 {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}

	user, err := h.userRepo.GetByID(c.Request.Context(), userID)
	if err != nil {
		render.RenderErrorPage(c, http.StatusNotFound)
		return
	}

	if !user.TwoFactorEnabled {
		c.Redirect(http.StatusFound, "/user/profile")
		return
	}

	render.Page(c, http.StatusOK, "user-2fa-disable.html", gin.H{
		"Title":         "Отключить 2FA",
		"User":          user.ToPublic(),
		"CurrentUserID": userID,
		"IsAdmin":       middleware.IsAdmin(c),
		"csrf":          csrf.GetToken(c),
	})
}

// Disable отключает 2FA.
// @Summary Отключение 2FA
// @Tags 2fa
// @Accept x-www-form-urlencoded
// @Produce html
// @Param code formData string true "Код подтверждения"
// @Success 302 {string} string "Перенаправление в профиль"
// @Failure 400 {object} map[string]interface{} render.Tr(c, "handler.invalid_code")
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /user/2fa/disable [post]
// @Security JWT
func (h *TwoFactorHandler) Disable(c *gin.Context) {
	userID := c.GetUint("userID")
	if userID == 0 {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}

	var input struct {
		Password string `form:"password" binding:"required"`
		Code     string `form:"code"`
	}
	if err := c.ShouldBind(&input); err != nil {
		render.SetFlash(c, "error", i18n.T("twofa.enter_password"))
		c.Redirect(http.StatusFound, "/user/2fa/disable")
		return
	}

	user, err := h.userRepo.GetByID(c.Request.Context(), userID)
	if err != nil {
		render.RenderErrorPage(c, http.StatusNotFound)
		return
	}

	// Проверяем пароль перед отключением 2FA
	baseData := gin.H{
		"Title":         "Отключить 2FA",
		"User":          user.ToPublic(),
		"CurrentUserID": userID,
		"IsAdmin":       middleware.IsAdmin(c),
		"csrf":          csrf.GetToken(c),
	}

	_, authErr := h.authService.Login(c.Request.Context(), user.Email, input.Password)
	if authErr != nil {
		data := baseData
		data["Error"] = render.Tr(c, "handler.wrong_password")
		render.Page(c, http.StatusOK, "user-2fa-disable.html", data)
		return
	}

	// Требуем текущий TOTP-код (S-L1): отключение 2FA по одному паролю
	// позволяло бы атакующему с украденной сессией+паролем снять защиту.
	if user.TwoFactorEnabled {
		valid, codeErr := h.twoFactorSvc.VerifyCode(user.TwoFactorSecret, strings.TrimSpace(input.Code))
		if codeErr != nil || !valid {
			data := baseData
			data["Error"] = render.Tr(c, "handler.invalid_code")
			render.Page(c, http.StatusOK, "user-2fa-disable.html", data)
			return
		}
	}

	// Отключаем 2FA
	h.twoFactorSvc.Disable2FA(user)

	// Сохраняем
	if err := h.userRepo.Update(c.Request.Context(), user.ID, map[string]any{
		"two_factor_enabled":      false,
		"two_factor_secret":       "",
		"two_factor_backup_codes": "",
	}); err != nil {
		data := baseData
		data["Error"] = render.LocalizeError(c, err.Error())
		render.Page(c, http.StatusOK, "user-2fa-disable.html", data)
		return
	}

	render.SetFlash(c, "success", i18n.T("twofa.success_disabled"))
	clear2FASessionFlag(c)
	c.Redirect(http.StatusFound, "/profile")
}
