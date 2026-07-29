// internal/domain/user/routes.go
package user

import (
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/pkg/audit"
	"gengine-0/internal/pkg/email"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterRoutes СЂРµРіРёСЃС‚СЂРёСЂСѓРµС‚ РІСЃРµ РјР°СЂС€СЂСѓС‚С‹ РїРѕР»СЊР·РѕРІР°С‚РµР»СЊСЃРєРѕРіРѕ РґРѕРјРµРЅР°.
func RegisterRoutes(
	r *gin.RouterGroup,
	cfg *config.Config,
	authSvc *AuthService,
	userSvc *UserService,
	passwordResetSvc *PasswordResetService,
	emailVerifSvc *EmailVerificationService,
	oauthSvc *OAuthService,
	auditSvc *audit.Service,
	db *gorm.DB,
	localStorage storage.FileStorage,
	emailSvc *email.EmailService,
	webauthnHandler *WebAuthnHandler,
	userRepo UserRepository,
) {
	authHandler := NewAuthHandler(cfg, authSvc, userSvc, passwordResetSvc, emailVerifSvc, oauthSvc, auditSvc, emailSvc)
	profileSvc := NewProfileService(db)
	profileHandler := NewProfileHandler(db, localStorage, authSvc, profileSvc, userSvc, cfg)
	achievementHandler := NewAchievementHandler(db)
	dashboardHandler := NewDashboardHandler(NewUserDashboardService(db), db)

	oauthRateLimit := middleware.LoginRateLimit(5*time.Minute, 5)

	authGroup := r.Group("/auth")
	{
		authGroup.GET("/login", authHandler.ShowLoginForm)

		authGroup.POST("/login", middleware.LoginRateLimit(5*time.Minute, 5), authHandler.Login)

		authGroup.POST("/refresh", authHandler.RefreshToken)

		authGroup.GET("/register", authHandler.ShowRegisterForm)

		authGroup.POST("/register", middleware.RegistrationRateLimit(10*time.Minute, 3), authHandler.Register)

		authGroup.POST("/logout", authHandler.Logout)

		authGroup.POST("/logout-all", middleware.AuthRequired(authSvc), authHandler.LogoutAll)

		authGroup.GET("/forgot", authHandler.ShowForgotForm)

		authGroup.POST("/forgot", middleware.PasswordResetRateLimit(1*time.Minute, 10), authHandler.ForgotPassword)

		authGroup.GET("/reset/:resetCode", authHandler.ShowResetForm)

		authGroup.POST("/reset", middleware.PasswordResetRateLimit(1*time.Minute, 5), authHandler.ResetPassword)

		authGroup.GET("/verify", authHandler.VerifyEmail)

		authGroup.GET("/oauth/:provider", oauthRateLimit, authHandler.OAuthLogin)

		authGroup.GET("/oauth/:provider/callback", oauthRateLimit, authHandler.OAuthCallback)

		// WebAuthn registration (authenticated)
		authGroup.POST("/webauthn/register/begin", middleware.AuthRequired(authSvc), webauthnHandler.BeginRegistration)
		authGroup.POST("/webauthn/register/finish", middleware.AuthRequired(authSvc), webauthnHandler.FinishRegistration)

		// WebAuthn login (public)
		authGroup.POST("/webauthn/login/begin", webauthnHandler.BeginLogin)
		authGroup.POST("/webauthn/login/finish", webauthnHandler.FinishLogin)
	}

	profileGroup := r.Group("/profile")
	profileGroup.Use(middleware.AuthRequired(authSvc))
	{
		profileGroup.GET("/", profileHandler.Show)

		profileGroup.POST("/avatar", profileHandler.UploadAvatar)

		profileGroup.POST("/update", profileHandler.UpdateProfile)

		profileGroup.POST("/change-password", profileHandler.ChangePassword)

		profileGroup.GET("/webauthn-keys", webauthnHandler.ListKeys)
		profileGroup.POST("/webauthn-keys/delete/:id", webauthnHandler.DeleteKey)
	}

	achievementGroup := r.Group("/achievements")
	achievementGroup.Use(middleware.AuthRequired(authSvc))
	{
		achievementGroup.GET("/", achievementHandler.List)
	}

	dashboardGroup := r.Group("/dashboard")
	dashboardGroup.Use(middleware.AuthRequired(authSvc))
	{
		dashboardGroup.GET("/", dashboardHandler.Index)
	}

	// ============================================================
	// РџРЈР‘Р›РР§РќР«Р™ РџР РћР¤РР›Р¬ РџРћР›Р¬Р—РћР’РђРўР•Р›РЇ
	// ============================================================
	usersGroup := r.Group("/users")
	usersGroup.Use(middleware.OptionalAuth(authSvc))
	{
		usersGroup.GET("/:id", profileHandler.PublicProfile)
	}

	// ============================================================
	// WEB PUSH РЈР’Р•Р”РћРњР›Р•РќРРЇ (API)
	// ============================================================
	pushHandler := NewPushHandler(db, cfg.VAPID)
	apiGroup := r.Group("/api/push")
	apiGroup.Use(middleware.AuthRequired(authSvc))
	{
		apiGroup.POST("/subscribe", pushHandler.Subscribe)
		apiGroup.POST("/unsubscribe", pushHandler.Unsubscribe)
		apiGroup.GET("/vapid-public-key", pushHandler.VapidPublicKey)
	}

	// ============================================================
	// 2FA (настройка)
	// ============================================================
	twoFactorSvc := NewTwoFactorService()
	twoFactorHandler := NewTwoFactorHandler(twoFactorSvc, authSvc, userRepo)
	userGroup := r.Group("/user")
	userGroup.Use(middleware.AuthRequired(authSvc))
	{
		userGroup.GET("/2fa/enable", twoFactorHandler.EnableForm)
		userGroup.POST("/2fa/enable", twoFactorHandler.Enable)
		userGroup.GET("/2fa/disable", twoFactorHandler.DisableForm)
		userGroup.POST("/2fa/disable", twoFactorHandler.Disable)
	}
}
