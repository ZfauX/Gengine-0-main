// internal/domain/notification/settings_handler.go
package notification

import (
	"net/http"

	"gengine-0/internal/pkg/render"

	"github.com/gin-gonic/gin"
)

// SettingsHandler обрабатывает настройки уведомлений пользователя
type SettingsHandler struct {
	svc *NotificationService
}

// NewSettingsHandler создаёт обработчик настроек
func NewSettingsHandler(svc *NotificationService) *SettingsHandler {
	return &SettingsHandler{svc: svc}
}

// ShowForm отображает страницу настроек уведомлений
func (h *SettingsHandler) ShowForm(c *gin.Context) {
	userID := c.GetUint("userID")

	settings, err := h.svc.GetSettings(c.Request.Context(), userID)
	if err != nil {
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	render.Page(c, http.StatusOK, "notification-settings.html", gin.H{
		"Settings":    settings,
		"CurrentUserID": userID,
		"Title":       "Настройки уведомлений",
		"Description": "Управление email и push-уведомлениями",
	})
}

// Save сохраняет настройки уведомлений
func (h *SettingsHandler) Save(c *gin.Context) {
	userID := c.GetUint("userID")

	var input struct {
		EmailEnabled         bool `form:"email_enabled"`
		BrowserEnabled       bool `form:"browser_enabled"`
		PushEnabled          bool `form:"push_enabled"`
		EmailGameStarted     bool `form:"email_game_started"`
		EmailLevelCompleted  bool `form:"email_level_completed"`
		EmailApplicationAccepted bool `form:"email_application_accepted"`
		EmailApplicationRejected bool `form:"email_application_rejected"`
		EmailTimeWarning     bool `form:"email_time_warning"`
		EmailTimeExpired     bool `form:"email_time_expired"`
	}

	if err := c.ShouldBind(&input); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверные данные формы")
		return
	}

	settings := &Settings{
		EmailEnabled:           input.EmailEnabled,
		BrowserEnabled:         input.BrowserEnabled,
		PushEnabled:            input.PushEnabled,
		EmailGameStarted:       input.EmailGameStarted,
		EmailLevelCompleted:    input.EmailLevelCompleted,
		EmailApplicationAccepted: input.EmailApplicationAccepted,
		EmailApplicationRejected: input.EmailApplicationRejected,
		EmailTimeWarning:       input.EmailTimeWarning,
		EmailTimeExpired:       input.EmailTimeExpired,
	}

	if err := h.svc.SaveSettings(c.Request.Context(), userID, settings); err != nil {
		render.RenderError(c, http.StatusInternalServerError, "Ошибка сохранения настроек")
		return
	}

	c.Redirect(http.StatusFound, "/settings/notifications")
}

// APIEmailFlags возвращает флаги email-уведомлений через API (для AJAX)
func (h *SettingsHandler) APIEmailFlags(c *gin.Context) {
	userID := c.GetUint("userID")

	flags, err := h.svc.GetEmailNotificationFlags(c.Request.Context(), userID)
	if err != nil {
		c.JSON(500, gin.H{"error": "ошибка загрузки настроек"})
		return
	}

	c.JSON(200, gin.H{"settings": flags})
}

// APIEmailSave сохраняет флаги email-уведомлений через API (для AJAX)
func (h *SettingsHandler) APIEmailSave(c *gin.Context) {
	userID := c.GetUint("userID")

	var input struct {
		EmailEnabled           bool `json:"email_enabled"`
		BrowserEnabled         bool `json:"browser_enabled"`
		EmailGameStarted       bool `json:"email_game_started"`
		EmailLevelCompleted    bool `json:"email_level_completed"`
		EmailApplicationAccepted bool `json:"email_application_accepted"`
		EmailApplicationRejected bool `json:"email_application_rejected"`
		EmailTimeWarning       bool `json:"email_time_warning"`
		EmailTimeExpired       bool `json:"email_time_expired"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(400, gin.H{"error": "неверные данные"})
		return
	}

	settings := &Settings{
		EmailEnabled:           input.EmailEnabled,
		BrowserEnabled:         input.BrowserEnabled,
		EmailGameStarted:       input.EmailGameStarted,
		EmailLevelCompleted:    input.EmailLevelCompleted,
		EmailApplicationAccepted: input.EmailApplicationAccepted,
		EmailApplicationRejected: input.EmailApplicationRejected,
		EmailTimeWarning:       input.EmailTimeWarning,
		EmailTimeExpired:       input.EmailTimeExpired,
	}

	if err := h.svc.SaveSettings(c.Request.Context(), userID, settings); err != nil {
		c.JSON(500, gin.H{"error": "ошибка сохранения"})
		return
	}

	c.JSON(200, gin.H{"success": true})
}
