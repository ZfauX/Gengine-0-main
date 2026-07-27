// internal/domain/user/two_factor_handler.go
package user

import (
	"gengine-0/internal/pkg/render"
	"net/http"

	csrf "gengine-0/internal/pkg/csrf"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// TwoFactorHandler обрабатывает HTTP-запросы для 2FA.
type TwoFactorHandler struct {
	twoFactorSvc *TwoFactorService
	authService  *AuthService
	userRepo     UserRepository
}

// NewTwoFactorHandler создаёт новый handler 2FA.
func NewTwoFactorHandler(twoFactorSvc *TwoFactorService, authService *AuthService, userRepo UserRepository) *TwoFactorHandler {
	return &TwoFactorHandler{
		twoFactorSvc: twoFactorSvc,
		authService:  authService,
		userRepo:     userRepo,
	}
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
			"Title": "2FA уже включена",
			"User":  user.ToPublic(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	// Генерируем секрет и QR-код
	secret, _ := h.twoFactorSvc.GenerateSecret()
	qrURL, _ := h.twoFactorSvc.GenerateQRCodeURL(secret, user.Email, "Gengine-0")

	// Сохраняем секрет в сессии для подтверждения на следующем шаге
	sess := sessions.Default(c)
	sess.Set("2fa_pending_secret", secret)
	sess.Set("2fa_pending_secret_url", qrURL)
	if err := sess.Save(); err != nil {
		log.Error().Err(err).Msg("EnableForm: failed to save session")
	}

	render.Page(c, http.StatusOK, "user-2fa-enable.html", gin.H{
		"Title":  "Включить 2FA",
		"User":   user.ToPublic(),
		"Secret": secret,
		"QRURL":  qrURL,
		"csrf":   csrf.GetToken(c),
	})
}

// Enable подтверждает включение 2FA.
// @Summary Подтверждение включения 2FA
// @Tags 2fa
// @Accept x-www-form-urlencoded
// @Produce html
// @Param code formData string true "Код подтверждения"
// @Success 302 {string} string "Перенаправление в профиль"
// @Failure 400 {object} map[string]interface{} "Неверный код"
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
			"Title": "Включить 2FA",
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
			"Title": "Включить 2FA",
			"Error": "Неверный код. Попробуйте ещё раз.",
			"User":  user.ToPublic(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	// Генерируем резервные коды
	backupCodes, err := h.twoFactorSvc.GenerateBackupCodes()
	if err != nil {
		render.Page(c, http.StatusOK, "user-2fa-enable.html", gin.H{
			"Title": "Включить 2FA",
			"Error": "Ошибка генерации резервных кодов: " + err.Error(),
			"User":  user.ToPublic(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	hashedCodes, err := h.twoFactorSvc.HashBackupCodes(backupCodes)
	if err != nil {
		render.Page(c, http.StatusOK, "user-2fa-enable.html", gin.H{
			"Title": "Включить 2FA",
			"Error": "Ошибка хеширования резервных кодов: " + err.Error(),
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
			"Title": "Включить 2FA",
			"Error": "Ошибка сохранения: " + err.Error(),
			"User":  user.ToPublic(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	// Очищаем сессию
	sess.Delete("2fa_pending_secret")
	sess.Delete("2fa_pending_secret_url")
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
		"Title": "Отключить 2FA",
		"User":  user.ToPublic(),
		"csrf":  csrf.GetToken(c),
	})
}

// Disable отключает 2FA.
// @Summary Отключение 2FA
// @Tags 2fa
// @Accept x-www-form-urlencoded
// @Produce html
// @Param code formData string true "Код подтверждения"
// @Success 302 {string} string "Перенаправление в профиль"
// @Failure 400 {object} map[string]interface{} "Неверный код"
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
	}
	if err := c.ShouldBind(&input); err != nil {
		render.SetFlash(c, "error", "Введите текущий пароль")
		c.Redirect(http.StatusFound, "/user/2fa/disable")
		return
	}

	user, err := h.userRepo.GetByID(c.Request.Context(), userID)
	if err != nil {
		render.RenderErrorPage(c, http.StatusNotFound)
		return
	}

	// Проверяем пароль перед отключением 2FA
	_, authErr := h.authService.Login(c.Request.Context(), user.Email, input.Password)
	if authErr != nil {
		render.Page(c, http.StatusOK, "user-2fa-disable.html", gin.H{
			"Title": "Отключить 2FA",
			"Error": "Неверный пароль.",
			"User":  user.ToPublic(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	// Отключаем 2FA
	h.twoFactorSvc.Disable2FA(user)

	// Сохраняем
	if err := h.userRepo.Update(c.Request.Context(), user.ID, map[string]any{
		"two_factor_enabled":      false,
		"two_factor_secret":       "",
		"two_factor_backup_codes": "",
	}); err != nil {
		render.Page(c, http.StatusOK, "user-2fa-disable.html", gin.H{
			"Title": "Отключить 2FA",
			"Error": "Ошибка сохранения: " + err.Error(),
			"User":  user.ToPublic(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	render.SetFlash(c, "success", "2FA отключена")
	c.Redirect(http.StatusFound, "/profile")
}
