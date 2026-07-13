// internal/domain/user/two_factor_handler.go
package user

import (
	"gengine-0/internal/pkg/render"
	"net/http"

	"github.com/gin-gonic/gin"
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
			"User":  user,
		})
		return
	}

	// Генерируем секрет и QR-код
	secret, _ := h.twoFactorSvc.GenerateSecret()
	qrURL, _ := h.twoFactorSvc.GenerateQRCodeURL(secret, user.Email, "Gengine-0")

	render.Page(c, http.StatusOK, "user-2fa-enable.html", gin.H{
		"Title":  "Включить 2FA",
		"User":   user,
		"Secret": secret,
		"QRURL":  qrURL,
	})
}

// Enable подтверждает включение 2FA.
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

	// Проверяем код
	valid, err := h.twoFactorSvc.VerifyCode(user.TwoFactorSecret, input.Code)
	if err != nil || !valid {
		render.Page(c, http.StatusOK, "user-2fa-enable.html", gin.H{
			"Title":  "Включить 2FA",
			"Error":  "Неверный код. Попробуйте ещё раз.",
			"User":   user,
			"Secret": user.TwoFactorSecret,
		})
		return
	}

	// Включаем 2FA
	if err := h.twoFactorSvc.Enable2FA(user); err != nil {
		render.Page(c, http.StatusOK, "user-2fa-enable.html", gin.H{
			"Title": "Включить 2FA",
			"Error": "Ошибка включения: " + err.Error(),
			"User":  user,
		})
		return
	}

	// Сохраняем пользователя
	if err := h.userRepo.Update(c.Request.Context(), user.ID, map[string]interface{}{
		"two_factor_enabled":      true,
		"two_factor_secret":       user.TwoFactorSecret,
		"two_factor_backup_codes": user.TwoFactorBackupCodes,
	}); err != nil {
		render.Page(c, http.StatusOK, "user-2fa-enable.html", gin.H{
			"Title": "Включить 2FA",
			"Error": "Ошибка сохранения: " + err.Error(),
			"User":  user,
		})
		return
	}

	c.Redirect(http.StatusFound, "/user/profile?2fa_enabled=1")
}

// DisableForm отображает форму отключения 2FA.
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
		"User":  user,
	})
}

// Disable отключает 2FA.
func (h *TwoFactorHandler) Disable(c *gin.Context) {
	userID := c.GetUint("userID")
	if userID == 0 {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}

	var input struct {
		Code string `form:"code" binding:"required"`
	}
	if err := c.ShouldBind(&input); err != nil {
		c.Redirect(http.StatusFound, "/user/2fa/disable")
		return
	}

	user, err := h.userRepo.GetByID(c.Request.Context(), userID)
	if err != nil {
		render.RenderErrorPage(c, http.StatusNotFound)
		return
	}

	// Проверяем TOTP-код
	valid, err := h.twoFactorSvc.VerifyCode(user.TwoFactorSecret, input.Code)
	if err != nil || !valid {
		render.Page(c, http.StatusOK, "user-2fa-disable.html", gin.H{
			"Title": "Отключить 2FA",
			"Error": "Неверный код.",
			"User":  user,
		})
		return
	}

	// Отключаем 2FA
	h.twoFactorSvc.Disable2FA(user)

	// Сохраняем
	if err := h.userRepo.Update(c.Request.Context(), user.ID, map[string]interface{}{
		"two_factor_enabled":      false,
		"two_factor_secret":       "",
		"two_factor_backup_codes": "",
	}); err != nil {
		render.Page(c, http.StatusOK, "user-2fa-disable.html", gin.H{
			"Title": "Отключить 2FA",
			"Error": "Ошибка сохранения: " + err.Error(),
			"User":  user,
		})
		return
	}

	c.Redirect(http.StatusFound, "/user/profile?2fa_disabled=1")
}
