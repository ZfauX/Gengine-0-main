// internal/domain/social/routes.go
package social

import (
	"gengine-0/internal/config"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterRoutes СЂРµРіРёСЃС‚СЂРёСЂСѓРµС‚ РјР°СЂС€СЂСѓС‚С‹ СЃРѕС†РёР°Р»СЊРЅС‹С… С„СѓРЅРєС†РёР№: РїРѕРґРїРёСЃРєРё.
func RegisterRoutes(
	router *gin.RouterGroup,
	db *gorm.DB,
	cfg *config.Config,
	authService *user.AuthService,
) {
	followRepo := NewGormFollowRepo(db)
	followService := NewFollowService(followRepo)
	followHandler := NewFollowHandler(followService)

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
