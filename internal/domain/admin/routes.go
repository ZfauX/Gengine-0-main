// internal/domain/admin/routes.go
package admin

import (
	"net/http"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/audit"
	"gengine-0/internal/pkg/cache"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/websocket"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterRoutes регистрирует маршруты административной панели.
// router — уже сгруппированный группа с /admin prefix и middleware (например 2FA).
func RegisterRoutes(
	router *gin.RouterGroup,
	db *gorm.DB,
	cfg *config.Config,
	authService *user.AuthService,
	userRepo user.UserRepository,
	gameRepo game.GameRepository,
	gameService *game.GameService,
	hub *websocket.RoomHub,
	auditService *audit.Service,
	cacheStore cache.CacheStore,
) {
	if auditService == nil {
		auditService = audit.NewService(db)
	}

	backupRepo := NewGormBackupRepo(db)
	backupService := NewBackupService(backupRepo, "backups", cfg.Server.MaxBackups, cfg.Database)

	teamRepo := team.NewGormTeamRepo(db)
	refreshTokenRepo := user.NewGormRefreshTokenRepo(db)
	adminHandler := NewAdminHandler(userRepo, gameRepo, gameService, teamRepo, backupService, auditService, refreshTokenRepo, cacheStore)

	authRequired := middleware.AuthRequired(authService)
	adminOnly := adminOnlyMiddleware()
	twoFactorMW := user.TwoFactorRequired(user.NewTwoFactorService(), userRepo)

	protected := router.Group("/")
	protected.Use(authRequired, twoFactorMW, adminOnly)
	{
		protected.GET("/", adminHandler.Dashboard)

		protected.GET("/users", adminHandler.ListUsers)

		protected.POST("/users/:id/toggle-admin", adminHandler.ToggleAdmin)

		protected.POST("/users/:id/delete", adminHandler.DeleteUser)

		protected.GET("/games", adminHandler.ListGames)

		protected.POST("/games/:id/delete", adminHandler.DeleteGame)

		protected.GET("/teams", adminHandler.ListTeams)

		protected.GET("/audit", adminHandler.AuditLog)

		protected.GET("/backups", adminHandler.ListBackups)

		protected.POST("/backups/create", adminHandler.CreateBackup)

		protected.GET("/backups/:id/download", adminHandler.DownloadBackup)

		protected.POST("/backups/rotate", adminHandler.RotateBackups)

		protected.GET("/ws-health", func(c *gin.Context) {
			if hub == nil {
				c.JSON(http.StatusOK, gin.H{"status": "healthy", "ws_status": "not_initialized"})
				return
			}
			stats := hub.GetHealthStatus()
			c.JSON(http.StatusOK, stats)
		})
	}
}

// adminOnlyMiddleware РїСЂРѕРІРµСЂСЏРµС‚, С‡С‚Рѕ РїРѕР»СЊР·РѕРІР°С‚РµР»СЊ СЏРІР»СЏРµС‚СЃСЏ Р°РґРјРёРЅРёСЃС‚СЂР°С‚РѕСЂРѕРј, РёСЃРїРѕР»СЊР·СѓСЏ СЂРѕР»СЊ РёР· РєРѕРЅС‚РµРєСЃС‚Р° (РёР· JWT).
// РќРµ С‚СЂРµР±СѓРµС‚ РїРµСЂРµРґР°С‡Рё *gorm.DB, С‚Р°Рє РєР°Рє СЂРѕР»СЊ СѓР¶Рµ СЃРѕС…СЂР°РЅРµРЅР° РІ РєРѕРЅС‚РµРєСЃС‚Рµ middleware.AuthRequired.
func adminOnlyMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, exists := c.Get("role")
		if !exists {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		roleStr, ok := role.(string)
		if !ok || roleStr != "admin" {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Set("IsAdmin", true)
		c.Next()
	}
}
