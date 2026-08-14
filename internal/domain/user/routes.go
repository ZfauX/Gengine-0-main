// internal/domain/user/routes.go
package user

import (
	"net/http"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/pkg/audit"
	"gengine-0/internal/pkg/email"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// RegisterRoutes регистрирует все маршруты пользовательского домена.
func RegisterRoutes(
	r *gin.RouterGroup,
	cfg *config.Config,
	authSvc *AuthService,
	userSvc *UserService,
	passwordResetSvc *PasswordResetService,
	emailVerifSvc *EmailVerificationService,
	oauthSvc *OAuthService,
	auditSvc *audit.Service,
	localStorage storage.FileStorage,
	emailSvc *email.EmailService,
	webauthnHandler *WebAuthnHandler,
	userRepo UserRepository,
	pushHandler *PushHandler,
	twoFactorSvc *TwoFactorService,
	profileSvc *ProfileService,
	achievRepo AchievementRepository,
	dashboardSvc *UserDashboardService,
) {
	// Inject server config for Secure cookie detection (handles reverse proxy)
	SetSecureCookieConfig = &cfg.Server

	authHandler := NewAuthHandler(cfg, authSvc, userSvc, passwordResetSvc, emailVerifSvc, oauthSvc, auditSvc, emailSvc, twoFactorSvc)
	profileHandler := NewProfileHandler(localStorage, authSvc, profileSvc, userSvc, cfg)
	achievementHandler := NewAchievementHandler(achievRepo)
	dashboardHandler := NewDashboardHandler(dashboardSvc)

	twoFactorHandler := NewTwoFactorHandler(twoFactorSvc, authSvc, userRepo, cfg.JWT.AccessExpiry)
	// UX-2 (PASS-13): «запомнить устройство 30 дней» — секрет для HMAC-подписи
	// trusted-device cookie (глобально для TwoFactorRequired middleware и в
	// хендлере для установки cookie).
	SetTrustedSecret(cfg.Session.Secret)
	twoFactorHandler.WithTrustedSecret(cfg.Session.Secret)
	// H6 (PASS-15): Secure-флаг trusted cookie — как у JWT/refresh (S-M4):
	// TLS / reverse-proxy / FORCE_SECURE_COOKIE. Иначе кука обхода 2FA уходит
	// по HTTP (MITM).
	trustedSecure := cfg.TLS.CertFile != "" || cfg.Server.TrustedProxies != "" || cfg.Server.ForceSecureCookie
	SetTrustedSecure(trustedSecure)
	twoFactorHandler.WithTrustedSecure(trustedSecure)

	// S-44-2 (pass 44): отдельный ключ OAuth — не делит бюджет с парольным логином.
	oauthRateLimit := middleware.OAuthRateLimit(5*time.Minute, 10)

	authGroup := r.Group("/auth")
	{
		authGroup.GET("/login", authHandler.ShowLoginForm)

		authGroup.POST("/login", middleware.LoginRateLimit(5*time.Minute, 5), authHandler.Login)

		authGroup.POST("/refresh", middleware.LoginRateLimit(1*time.Minute, 10), authHandler.RefreshToken)

		authGroup.GET("/register", authHandler.ShowRegisterForm)

		authGroup.POST("/register", middleware.RegistrationRateLimit(10*time.Minute, 3), authHandler.Register)

		authGroup.POST("/logout", authHandler.Logout)

		authGroup.POST("/logout-all", middleware.AuthRequired(authSvc), authHandler.LogoutAll)

		authGroup.GET("/forgot", authHandler.ShowForgotForm)

		authGroup.POST("/forgot", middleware.PasswordResetRateLimit(1*time.Minute, 10), authHandler.ForgotPassword)

		authGroup.GET("/reset/:resetCode", authHandler.ShowResetForm)

		authGroup.POST("/reset", middleware.PasswordResetRateLimit(1*time.Minute, 5), authHandler.ResetPassword)

		// L5 (PASS-10): GET-форма ввода verify-кода (письмо ведёт сюда без кода в URL).
		authGroup.GET("/verify", authHandler.VerifyEmailForm)
		authGroup.POST("/verify", middleware.PasswordResetRateLimit(1*time.Minute, 10), authHandler.VerifyEmail)

		// 2FA login verification (public — used after password login)
		authGroup.GET("/2fa/login", authHandler.TwoFALoginForm)
		authGroup.POST("/2fa/login", middleware.LoginRateLimit(5*time.Minute, 5), authHandler.TwoFALoginVerify)

		// 2FA verification routes (authenticated — used for existing sessions)
		authGroup.GET("/2fa/verify", middleware.AuthRequired(authSvc), twoFactorHandler.VerifyForm)
		authGroup.POST("/2fa/verify", middleware.LoginRateLimit(5*time.Minute, 5), twoFactorHandler.Verify)
		authGroup.GET("/2fa/backup", middleware.AuthRequired(authSvc), twoFactorHandler.BackupForm)
		authGroup.POST("/2fa/backup", middleware.LoginRateLimit(5*time.Minute, 5), twoFactorHandler.BackupVerify)

		authGroup.GET("/oauth/:provider", oauthRateLimit, authHandler.OAuthLogin)

		authGroup.GET("/oauth/:provider/callback", oauthRateLimit, authHandler.OAuthCallback)

		// WebAuthn registration (authenticated)
		authGroup.POST("/webauthn/register/begin", middleware.AuthRequired(authSvc), webauthnHandler.BeginRegistration)
		authGroup.POST("/webauthn/register/finish", middleware.AuthRequired(authSvc), webauthnHandler.FinishRegistration)

		// WebAuthn login (public)
		// DEEP-REVIEW PASS-2 (#5): rate-limit на публичные begin/finish —
		// раньше флуд begin писал сессии и грузил CPU (публичный, CSRF-exempt).
		// S-M1 (PASS-8): отдельный бюджет — раньше делил login:<ip> (спам begin
		// блокировал парольный вход всем за NAT).
		authGroup.POST("/webauthn/login/begin", middleware.WebAuthnRateLimit(1*time.Minute, 10), webauthnHandler.BeginLogin)
		authGroup.POST("/webauthn/login/finish", middleware.WebAuthnRateLimit(1*time.Minute, 10), webauthnHandler.FinishLogin)
	}

	// Profile routes — require auth
	profileGroup := r.Group("/profile")
	profileGroup.Use(middleware.AuthRequired(authSvc))
	{
		profileGroup.GET("/", profileHandler.Show)

		profileGroup.POST("/avatar", middleware.UploadRateLimit(5*time.Minute, 20), profileHandler.UploadAvatar)

		profileGroup.POST("/update", middleware.UploadRateLimit(5*time.Minute, 20), profileHandler.UpdateProfile)

		profileGroup.POST("/theme-settings", profileHandler.UpdateThemeSettings)

		// D-1 (pass 45): период уведомлений о предстоящих играх.
		profileGroup.POST("/notify-game-days", profileHandler.UpdateNotifyGameDays)

		// S-1 (pass 35): rate limit на смену пароля — дополнительная защита от
		// перебора старого пароля (lockout в сервисе + IP-лимит).
		profileGroup.POST("/change-password", middleware.LoginRateLimit(5*time.Minute, 5), profileHandler.ChangePassword)

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
	// ПУБЛИЧНЫЙ ПРОФИЛЬ ПОЛЬЗОВАТЕЛЯ
	// ============================================================
	usersGroup := r.Group("/users")
	usersGroup.Use(middleware.OptionalAuth(authSvc))
	{
		usersGroup.GET("/:id", profileHandler.PublicProfile)
	}

	// ============================================================
	// API
	// ============================================================
	apiR := r.Group("/api")
	{
		// #9: публичный поиск пользователей — dedicated per-IP лимитер.
		// M16 (pass 30): используем DI-репозиторий вместо NewGormUserRepo(db).
		// S-M1 (PASS-8): SearchRateLimit — отдельный бюджет, не делит login:<ip>.
		apiR.GET("/users/search", middleware.SearchRateLimit(time.Minute, 20), SearchUsersAPI(userRepo))
	}

	// Предпочтения пользователя (серверная персонализация)
	prefsAPI := r.Group("/api/users/preferences")
	prefsAPI.Use(middleware.AuthRequired(authSvc))
	{
		prefsAPI.GET("/games-view", func(c *gin.Context) {
			userID := c.GetUint("userID")
			view, err := profileSvc.GetGamesView(c.Request.Context(), userID)
			if err != nil {
				log.Error().Err(err).Uint("user_id", userID).Msg("games-view: failed to get preference")
				c.JSON(http.StatusInternalServerError, gin.H{"error": "ошибка загрузки предпочтения"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"view": view})
		})
		prefsAPI.PUT("/games-view", func(c *gin.Context) {
			userID := c.GetUint("userID")
			var req struct {
				View string `json:"view"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "неверный формат"})
				return
			}
			if err := profileSvc.SaveGamesView(c.Request.Context(), userID, req.View); err != nil {
				log.Error().Err(err).Uint("user_id", userID).Msg("games-view: failed to save preference")
				c.JSON(http.StatusInternalServerError, gin.H{"error": "ошибка сохранения предпочтения"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
		})
	}

	// ============================================================
	// WEB PUSH УВЕДОМЛЕНИЯ (API)
	// ============================================================
	apiGroup := r.Group("/api/push")
	apiGroup.Use(middleware.AuthRequired(authSvc))
	{
		apiGroup.POST("/subscribe", pushHandler.Subscribe)
		apiGroup.POST("/unsubscribe", pushHandler.Unsubscribe)
		apiGroup.GET("/vapid-public-key", pushHandler.VapidPublicKey)
	}

	// ============================================================
	// 2FA (настройка) — защищены 2FA если уже включена
	// ============================================================
	userGroup := r.Group("/user")
	userGroup.Use(middleware.AuthRequired(authSvc))
	{
		userGroup.GET("/2fa/enable", twoFactorHandler.EnableForm)
		// S-2 (pass 36): rate limit на включение 2FA (пароль + TOTP перебираются
		// с lockout в хендлере; IP-лимит — вторая линия защиты).
		userGroup.POST("/2fa/enable", middleware.LoginRateLimit(5*time.Minute, 5), twoFactorHandler.Enable)
		userGroup.GET("/2fa/disable", twoFactorHandler.DisableForm)
		// S-2 (pass 35): rate limit на отключение 2FA (пароль + TOTP перебираются
		// с lockout в хендлере; IP-лимит — вторая линия защиты).
		userGroup.POST("/2fa/disable", middleware.LoginRateLimit(5*time.Minute, 5), twoFactorHandler.Disable)
		// CRIT-1: QR-код генерируется локально, секрет не уходит наружу.
		userGroup.GET("/2fa/qr", twoFactorHandler.QRCode)
	}
}
