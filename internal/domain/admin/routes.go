// internal/domain/admin/routes.go
package admin

import (
	"net/http"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/user" // убрать импорт user? но мы передаём authService
	"gengine-0/internal/pkg/audit"
	"gengine-0/internal/pkg/middleware"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterRoutes теперь принимает authService (уже созданный), чтобы не создавать его внутри
func RegisterRoutes(router *gin.Engine, db *gorm.DB, cfg *config.Config, authService *user.AuthService) *audit.Service {
	auditService := audit.NewService(db)
	backupService := NewBackupService(db, "backups", cfg.Server.MaxBackups, cfg.Database)
	adminHandler := NewAdminHandler(db, backupService, auditService)

	// Используем переданный authService, не создаём новый
	authRequired := middleware.AuthRequired(authService)
	adminOnly := adminOnlyMiddleware(db) // оставляем как есть

	protected := router.Group("/admin")
	protected.Use(authRequired, adminOnly)
	{
		protected.GET("/", adminHandler.Dashboard)
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

	return auditService
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
		c.Set("IsAdmin", true)
		c.Next()
	}
}
