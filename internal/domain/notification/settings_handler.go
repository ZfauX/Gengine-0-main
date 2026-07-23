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

// ShowForm отображает страницу настроек уведомлений.
// @Summary Страница настроек уведомлений
// @Tags notifications
// @Produce html
// @Success 200 {string} html "Страница настроек"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /settings/notifications [get]
// @Security JWT
func (h *SettingsHandler) ShowForm(c *gin.Context) {
	userID := c.GetUint("userID")

	settings, err := h.svc.GetSettings(c.Request.Context(), userID)
	if err != nil {
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	render.Page(c, http.StatusOK, "notification-settings.html", gin.H{
		"Settings":      settings,
		"CurrentUserID": userID,
		"Title":         "Настройки уведомлений",
		"Description":   "Управление email и push-уведомлениями",
	})
}

// Save сохраняет настройки уведомлений.
// @Summary Сохранить настройки уведомлений
// @Tags notifications
// @Accept x-www-form-urlencoded
// @Produce html
// @Success 302 {string} string "Перенаправление на /settings/notifications"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /settings/notifications [post]
// @Security JWT
func (h *SettingsHandler) Save(c *gin.Context) {
	userID := c.GetUint("userID")

	var input struct {
		EmailEnabled             bool `form:"email_enabled"`
		BrowserEnabled           bool `form:"browser_enabled"`
		PushEnabled              bool `form:"push_enabled"`
		EmailGameStarted         bool `form:"email_game_started"`
		EmailLevelCompleted      bool `form:"email_level_completed"`
		EmailApplicationAccepted bool `form:"email_application_accepted"`
		EmailApplicationRejected bool `form:"email_application_rejected"`
		EmailTimeWarning         bool `form:"email_time_warning"`
		EmailTimeExpired         bool `form:"email_time_expired"`
	}

	if err := c.ShouldBind(&input); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверные данные формы")
		return
	}

	settings := &Settings{
		EmailEnabled:             input.EmailEnabled,
		BrowserEnabled:           input.BrowserEnabled,
		PushEnabled:              input.PushEnabled,
		EmailGameStarted:         input.EmailGameStarted,
		EmailLevelCompleted:      input.EmailLevelCompleted,
		EmailApplicationAccepted: input.EmailApplicationAccepted,
		EmailApplicationRejected: input.EmailApplicationRejected,
		EmailTimeWarning:         input.EmailTimeWarning,
		EmailTimeExpired:         input.EmailTimeExpired,
	}

	if err := h.svc.SaveSettings(c.Request.Context(), userID, settings); err != nil {
		render.RenderError(c, http.StatusInternalServerError, "Ошибка сохранения настроек")
		return
	}

	c.Redirect(http.StatusFound, "/settings/notifications")
}

// APIEmailFlags возвращает флаги email-уведомлений для текущего пользователя.
// @Summary Получить флаги email-уведомлений
// @Tags notifications
// @Produce json
// @Success 200 {object} map[string]interface{} "Флаги уведомлений"
// @Router /api/settings/notifications [get]
func (h *SettingsHandler) APIEmailFlags(c *gin.Context) {
	userID := c.GetUint("userID")

	flags, err := h.svc.GetEmailNotificationFlags(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "ошибка загрузки настроек"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"settings": flags})
}

// APIEmailSave сохраняет флаги email-уведомлений.
// @Summary Сохранить флаги email-уведомлений
// @Tags notifications
// @Accept json
// @Produce json
// @Param settings body map[string]interface{} true "Настройки уведомлений"
// @Success 200 {object} map[string]interface{} "Настройки сохранены"
// @Router /api/settings/notifications [post]
func (h *SettingsHandler) APIEmailSave(c *gin.Context) {
	userID := c.GetUint("userID")

	var input struct {
		EmailEnabled             bool `json:"email_enabled"`
		BrowserEnabled           bool `json:"browser_enabled"`
		EmailGameStarted         bool `json:"email_game_started"`
		EmailLevelCompleted      bool `json:"email_level_completed"`
		EmailApplicationAccepted bool `json:"email_application_accepted"`
		EmailApplicationRejected bool `json:"email_application_rejected"`
		EmailTimeWarning         bool `json:"email_time_warning"`
		EmailTimeExpired         bool `json:"email_time_expired"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "неверные данные"})
		return
	}

	settings := &Settings{
		EmailEnabled:             input.EmailEnabled,
		BrowserEnabled:           input.BrowserEnabled,
		EmailGameStarted:         input.EmailGameStarted,
		EmailLevelCompleted:      input.EmailLevelCompleted,
		EmailApplicationAccepted: input.EmailApplicationAccepted,
		EmailApplicationRejected: input.EmailApplicationRejected,
		EmailTimeWarning:         input.EmailTimeWarning,
		EmailTimeExpired:         input.EmailTimeExpired,
	}

	if err := h.svc.SaveSettings(c.Request.Context(), userID, settings); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "ошибка сохранения"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}
