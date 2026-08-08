// internal/domain/social/routes.go
package social

import (
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes регистрирует маршруты социальных функций: подписки.
func RegisterRoutes(
	router *gin.RouterGroup,
	followService *FollowService,
	authService *user.AuthService,
	userService *user.UserService,
) {
	followHandler := NewFollowHandler(followService, userService)

	authRequired := middleware.AuthRequired(authService)

	protected := router.Group("/")
	protected.Use(authRequired)
	{
		protected.POST("/follow/:id", followHandler.Follow)

		protected.DELETE("/follow/:id", followHandler.Unfollow)

		protected.GET("/follow/:id/check", followHandler.IsFollowing)

		protected.GET("/subscriptions", followHandler.Subscriptions)
	}
}
