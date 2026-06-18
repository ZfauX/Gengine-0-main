// internal/domain/admin/routes.go
package admin

import (
	"net/http"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func RegisterRoutes(router *gin.Engine, db *gorm.DB, cfg *config.Config) {
	auditService := NewAuditService(db)
	backupService := NewBackupService(db, "backups", cfg.Server.MaxBackups)
	adminHandler := NewAdminHandler(db, backupService, auditService)

	authService := user.NewAuthService(db, cfg)
	authRequired := middleware.AuthRequired(authService)
	adminOnly := adminOnlyMiddleware(db)

	protected := router.Group("/admin")
	protected.Use(authRequired, adminOnly)
	{
		protected.GET("/users", adminHandler.ListUsers)
		protected.GET("/users/:id/toggle-admin", adminHandler.ToggleAdmin)
		protected.GET("/users/:id/delete", adminHandler.DeleteUser)
		protected.GET("/games", adminHandler.ListGames)
		protected.GET("/games/:id/delete", adminHandler.DeleteGame)
		protected.GET("/audit", adminHandler.AuditLog)
		protected.GET("/backups", adminHandler.ListBackups)
		protected.POST("/backups/create", adminHandler.CreateBackup)
		protected.GET("/backups/:id/download", adminHandler.DownloadBackup)
		protected.POST("/backups/rotate", adminHandler.RotateBackups)
	}
}

func adminOnlyMiddleware(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		if userID == 0 {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		var role string
		err := db.Table("users").Select("role").Where("id = ?", userID).Scan(&role).Error
		if err != nil || role != "admin" {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
}