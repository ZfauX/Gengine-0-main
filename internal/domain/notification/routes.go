// internal/domain/notification/routes.go
package notification

import (
	"net/http"

	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// RegisterRoutes регистрирует API-маршруты для уведомлений.
func RegisterRoutes(r *gin.RouterGroup, db *gorm.DB, authService *user.AuthService, hub *ws.RoomHub) {
	service := NewNotificationService(db, hub).WithHub(hub)
	settingsHandler := NewSettingsHandler(service)

	// HTML-маршруты для страницы настроек уведомлений
	protected := r.Group("/settings")
	protected.Use(middleware.AuthRequired(authService))
	{
		// @Summary Страница настроек уведомлений
		// @Description Отображает страницу управления email и push-уведомлениями
		// @Tags notifications
		// @Produce html
		// @Success 200 {string} html "Страница настроек"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Router /settings/notifications [get]
		// @Security JWT
		protected.GET("/notifications", settingsHandler.ShowForm)
	}

	// Форма сохранения настроек (POST)
	protected.POST("/notifications", settingsHandler.Save)

	// API-маршруты для AJAX-операций
	api := r.Group("/api/settings")
	api.Use(middleware.AuthRequired(authService))
	{
		// @Summary Получить флаги email-уведомлений
		// @Description Возвращает гранулярные настройки email-уведомлений
		// @Tags notifications
		// @Produce json
		// @Success 200 {object} map[string]interface{} "Флаги"
		// @Router /api/settings/notifications [get]
		api.GET("/notifications", settingsHandler.APIEmailFlags)

		// @Summary Сохранить флаги email-уведомлений
		// @Description Сохраняет гранулярные настройки email-уведомлений
		// @Tags notifications
		// @Accept json
		// @Produce json
		// @Param settings body map[string]interface{} "Настройки"
		// @Success 200 {object} map[string]interface{} "Статус"
		// @Router /api/settings/notifications [post]
		api.POST("/notifications", settingsHandler.APIEmailSave)
	}

	// Группа с обязательной аутентификацией
	apiNotifs := r.Group("/api/notifications")
	apiNotifs.Use(middleware.AuthRequired(authService))
	{
		// @Summary Получить настройки уведомлений
		// @Description Возвращает текущие настройки уведомлений пользователя
		// @Tags notifications
		// @Produce json
		// @Success 200 {object} Settings
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Router /api/notifications/settings [get]
		api.GET("/settings", func(c *gin.Context) {
			userID := c.GetUint("userID")
			if userID == 0 {
				c.JSON(http.StatusUnauthorized, gin.H{
					"error": "Требуется аутентификация",
					"code":  "unauthorized",
				})
				return
			}
			settings, err := service.GetSettings(c.Request.Context(), userID)
			if err != nil {
				log.Error().Err(err).Uint("user_id", userID).Msg("Failed to get notification settings")
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "Внутренняя ошибка",
					"code":  "internal_error",
				})
				return
			}
			c.JSON(http.StatusOK, settings)
		})

		// @Summary Обновить настройки уведомлений
		// @Description Сохраняет настройки уведомлений пользователя
		// @Tags notifications
		// @Accept json
		// @Produce json
		// @Param settings body Settings true "Настройки"
		// @Success 200 {object} map[string]interface{} "Статус"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Router /api/notifications/settings [put]
		api.PUT("/settings", func(c *gin.Context) {
			userID := c.GetUint("userID")
			if userID == 0 {
				c.JSON(http.StatusUnauthorized, gin.H{
					"error": "Требуется аутентификация",
					"code":  "unauthorized",
				})
				return
			}

			var settings Settings
			if err := c.ShouldBindJSON(&settings); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{
					"error": "Неверный формат данных: " + err.Error(),
					"code":  "bad_request",
				})
				return
			}

			if err := service.SaveSettings(c.Request.Context(), userID, &settings); err != nil {
				log.Error().Err(err).Uint("user_id", userID).Msg("Failed to save notification settings")
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "Внутренняя ошибка",
					"code":  "internal_error",
				})
				return
			}

			c.JSON(http.StatusOK, gin.H{
				"status":  "ok",
				"message": "Настройки сохранены",
			})
		})
	}
}
