// internal/domain/user/routes.go
package user

import (
	"gengine-0/internal/config"
	"gengine-0/internal/pkg/audit"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func RegisterRoutes(router *gin.Engine, db *gorm.DB, cfg *config.Config, auditSvc *audit.Service) {
	// Инициализация сервисов
	authService := NewAuthService(db, cfg)
	dashboardService := NewUserDashboardService(db)

	// Инициализация хранилища для аватаров
	avatarStorage := storage.NewLocalStorage()

	// Обработчики
	authHandler := NewAuthHandler(db, cfg, auditSvc)
	profileHandler := &ProfileHandler{db: db, storage: avatarStorage}
	achievementHandler := &AchievementHandler{db: db}
	dashboardHandler := NewDashboardHandler(dashboardService, db)

	// Middleware
	optionalAuth := middleware.OptionalAuth(authService)

	// Rate limiting для чувствительных эндпоинтов
	loginLimiter := middleware.LoginRateLimit(1*time.Minute, 5)
	globalLimiter := middleware.GlobalRateLimit(1*time.Minute, 100)

	// Публичные маршруты (без аутентификации)
	router.GET("/auth/login", authHandler.ShowLoginForm)
	router.POST("/auth/login", loginLimiter, authHandler.Login)
	router.GET("/auth/register", authHandler.ShowRegisterForm)
	router.POST("/auth/register", loginLimiter, authHandler.Register)
	router.GET("/auth/forgot", authHandler.ShowForgotForm)
	router.POST("/auth/forgot", globalLimiter, authHandler.ForgotPassword)
	router.GET("/auth/reset", authHandler.ShowResetForm)
	router.POST("/auth/reset", globalLimiter, authHandler.ResetPassword)
	router.GET("/auth/verify", authHandler.VerifyEmail)

	// OAuth
	router.GET("/auth/oauth/:provider", authHandler.OAuthLogin)
	router.GET("/auth/oauth/:provider/callback", authHandler.OAuthCallback)

	// Выход из системы
	router.GET("/auth/logout", authHandler.Logout)

	// Публичный профиль пользователя (доступен всем, но с опциональной авторизацией)
	router.GET("/users/:id", optionalAuth, profileHandler.PublicProfile)

	// Защищённые маршруты (требуют JWT)
	protected := router.Group("/")
	protected.Use(middleware.AuthRequired(authService))
	{
		// Дашборд
		protected.GET("/dashboard", dashboardHandler.Index)

		// Профиль
		protected.GET("/profile", profileHandler.Show)
		protected.GET("/profile/public/:id", profileHandler.PublicProfile) // оставлен для обратной совместимости
		protected.POST("/profile/update", profileHandler.UpdateProfile)
		protected.POST("/profile/change-password", profileHandler.ChangePassword)
		protected.POST("/profile/upload-avatar", profileHandler.UploadAvatar)

		// Достижения
		protected.GET("/achievements", achievementHandler.List)
	}
}