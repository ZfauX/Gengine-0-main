// internal/domain/social/routes.go
package social

import (
	"gengine-0/internal/config"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterRoutes регистрирует маршруты социальных функций: подписки.
// Принимает готовый authService для избежания дублирования инициализации.
func RegisterRoutes(
	router *gin.Engine,
	db *gorm.DB,
	cfg *config.Config,
	authService *user.AuthService, // добавлен параметр
) {
	// Сервисы, оставшиеся в social
	followService := NewFollowService(db)

	// Обработчики
	followHandler := NewFollowHandler(followService)

	authRequired := middleware.AuthRequired(authService)

	// Защищённые маршруты
	protected := router.Group("/")
	protected.Use(authRequired)
	{
		// Подписки
		protected.POST("/follow/:id", followHandler.Follow)
		protected.DELETE("/follow/:id", followHandler.Unfollow)
		protected.GET("/follow/:id/check", followHandler.IsFollowing)
		protected.GET("/subscriptions", followHandler.Subscriptions)
	}
}
