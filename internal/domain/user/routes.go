// internal/domain/user/routes.go
package user

import (
	"gengine-0/internal/config"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func RegisterRoutes(router *gin.Engine, db *gorm.DB, cfg *config.Config) {
	// Инициализация сервисов
	authService := NewAuthService(db, cfg)
	dashboardService := NewUserDashboardService(db)

	// Инициализация хранилища для аватаров
	avatarStorage := storage.NewLocalStorage()

	// Обработчики
	authHandler := &AuthHandler{db: db, cfg: cfg}
	profileHandler := &ProfileHandler{db: db, storage: avatarStorage}
	achievementHandler := &AchievementHandler{db: db}
	dashboardHandler := NewDashboardHandler(dashboardService)

	// Middleware
	optionalAuth := middleware.OptionalAuth(authService)

	// Публичные маршруты (без аутентификации)
	router.GET("/auth/login", authHandler.ShowLoginForm)
	router.POST("/auth/login", authHandler.Login)
	router.GET("/auth/register", authHandler.ShowRegisterForm)
	router.POST("/auth/register", authHandler.Register)
	router.GET("/auth/forgot", authHandler.ShowForgotForm)
	router.POST("/auth/forgot", authHandler.ForgotPassword)
	router.GET("/auth/reset", authHandler.ShowResetForm)
	router.POST("/auth/reset", authHandler.ResetPassword)
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