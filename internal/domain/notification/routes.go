// internal/domain/notification/routes.go
package notification

import (
	"net/http"

	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// RegisterRoutes регистрирует API-маршруты для уведомлений.
func RegisterRoutes(r *gin.Engine, db *gorm.DB, authService *user.AuthService) {
	service := NewNotificationService(db)

	// Группа с обязательной аутентификацией
	api := r.Group("/api/notifications")
	api.Use(middleware.AuthRequired(authService)) // <-- ДОБАВЛЕНО
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
