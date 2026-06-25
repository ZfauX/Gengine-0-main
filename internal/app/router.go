// internal/app/router.go
package app

import (
	"fmt"
	"html/template"
	"path/filepath"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/admin"
	"gengine-0/internal/domain/calendar"
	"gengine-0/internal/domain/export"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/monitor"
	"gengine-0/internal/domain/social"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/tournament"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	_ "gengine-0/docs" // автоматически генерируется Swagger-документация

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	csrf "github.com/utrack/gin-csrf"
	"gorm.io/gorm"
)

func SetupRouter(db *gorm.DB, localStorage storage.FileStorage, hub *ws.RoomHub, cfg *config.Config, baseDir string) *gin.Engine {
	store := cookie.NewStore([]byte(cfg.Session.Secret))
	r := gin.New()

	r.Use(gin.Recovery())
	r.Use(sessions.Sessions("gengine_session", store))
	r.Use(csrf.Middleware(csrf.Options{
		Secret: cfg.Session.Secret,
		ErrorFunc: func(c *gin.Context) {
			c.String(403, "CSRF token mismatch")
			c.Abort()
		},
	}))

	r.FuncMap["add1"] = func(i int) int { return i + 1 }
	r.FuncMap["sub"] = func(a, b int) int { return a - b }
	r.FuncMap["add"] = func(a, b int) int { return a + b }
	r.FuncMap["loop"] = func(start, end int) []int {
		s := make([]int, end-start+1)
		for i := range s {
			s[i] = start + i
		}
		return s
	}
	r.FuncMap["formatBytes"] = func(b int64) string {
		const unit = 1024
		if b < unit {
			return fmt.Sprintf("%d B", b)
		}
		div, exp := int64(unit), 0
		for n := b / unit; n >= unit; n /= unit {
			div *= unit
			exp++
		}
		return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
	}
	r.FuncMap["csrfToken"] = func() string { return "{{ .csrf }}" }

	r.SetFuncMap(template.FuncMap(r.FuncMap))
	r.LoadHTMLGlob(filepath.Join(baseDir, "internal", "domain", "*", "templates", "*.html"))
	r.Use(middleware.ContextTimeout(30 * time.Second))
	r.Use(middleware.SecurityHeadersMiddleware())
	r.Use(middleware.GzipMiddleware())
	r.Use(middleware.StaticCacheMiddleware())

	r.Static("/static", filepath.Join(baseDir, "static"))
	r.Static("/uploads", filepath.Join(baseDir, "uploads"))

	// Swagger UI
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	// ================== СОЗДАНИЕ РЕПОЗИТОРИЕВ ==================

	// User
	userRepo := user.NewGormUserRepo(db)
	achievRepo := user.NewGormAchievementRepo(db)
	passResetRepo := user.NewGormPasswordResetRepo(db)
	emailVerifRepo := user.NewGormEmailVerificationRepo(db)
	extLoginRepo := user.NewGormExternalLoginRepo(db)

	// Game
	gameRepo := game.NewGormGameRepo(db)
	gamePassingRepo := game.NewGormGamePassingRepo(db)

	// Level
	levelRepo := level.NewGormLevelRepo(db)
	questionRepo := level.NewGormQuestionRepo(db)
	answerRepo := level.NewGormAnswerRepo(db)

	// Team
	teamRepo := team.NewGormTeamRepo(db)
	invitationRepo := team.NewGormInvitationRepo(db)

	// Tournament
	tournamentRepo := tournament.NewGormTournamentRepo(db)
	tournamentGameRepo := tournament.NewGormTournamentGameRepo(db)
	tournamentTeamRepo := tournament.NewGormTournamentTeamRepo(db)
	tournamentResultRepo := tournament.NewGormTournamentResultRepo(db)

	// ================== СОЗДАНИЕ СЕРВИСОВ ==================

	// User
	authService := user.NewAuthService(userRepo, achievRepo, emailVerifRepo, cfg)
	userService := user.NewUserService(userRepo)
	achievService := user.NewAchievementService(achievRepo)
	oauthService := user.NewOAuthService(userRepo, extLoginRepo, cfg)
	passwordResetService := user.NewPasswordResetService(userRepo, passResetRepo, cfg)
	emailVerifService := user.NewEmailVerificationService(userRepo, emailVerifRepo, cfg)

	// Game
	coAuthorSvc := game.NewCoAuthorService(db)
	reviewSvc := game.NewReviewService(db)
	attemptSvc := game.NewAttemptService(db)
	progressSvc := game.NewLevelProgressService(db)
	monitorSvc := game.NewMonitorService(db)
	gameSvc := game.NewGameService(
		gameRepo,
		gamePassingRepo,
		coAuthorSvc,
		reviewSvc,
		monitorSvc,
		hub,
		attemptSvc,
		progressSvc,
		cfg,
	)

	// Level
	levelSvc := level.NewLevelService(levelRepo, questionRepo, answerRepo, coAuthorSvc, gameSvc)
	questionSvc := level.NewQuestionService(questionRepo, levelRepo, coAuthorSvc)
	answerSvc := level.NewAnswerService(answerRepo, questionRepo, levelRepo, coAuthorSvc)

	// Team
	teamSvc := team.NewTeamService(teamRepo, coAuthorSvc)
	invitationSvc := team.NewInvitationService(invitationRepo, teamRepo, coAuthorSvc, cfg)

	// Tournament
	tournamentSvc := tournament.NewTournamentService(
		tournamentRepo,
		tournamentGameRepo,
		tournamentTeamRepo,
		tournamentResultRepo,
		teamSvc,
		cfg,
	)

	// ================== РЕГИСТРАЦИЯ МАРШРУТОВ ==================

	// Admin
	auditSvc := admin.RegisterRoutes(r, db, cfg, authService, userRepo, gameRepo)

	// User
	user.RegisterRoutes(r, authService, userService, achievService, oauthService, passwordResetService, emailVerifService, cfg, auditSvc, db)

	// Хендлеры user
	authHandler := user.NewAuthHandler(cfg, authService, userService, passwordResetService, emailVerifService, oauthService, auditSvc)
	profileHandler := user.NewProfileHandler(db, localStorage)
	achievementHandler := user.NewAchievementHandler(db)
	dashboardHandler := user.NewDashboardHandler(user.NewUserDashboardService(db), db)

	// Auth
	authGroup := r.Group("/auth")
	{
		authGroup.GET("/login", authHandler.ShowLoginForm)
		// Добавлен rate limit для логина: 5 попыток за 5 минут
		authGroup.POST("/login", middleware.LoginRateLimit(5*time.Minute, 5), authHandler.Login)
		authGroup.GET("/register", authHandler.ShowRegisterForm)
		authGroup.POST("/register", authHandler.Register)
		authGroup.GET("/logout", authHandler.Logout)
		authGroup.GET("/forgot", authHandler.ShowForgotForm)
		authGroup.POST("/forgot", authHandler.ForgotPassword)
		authGroup.GET("/reset", authHandler.ShowResetForm)
		authGroup.POST("/reset", authHandler.ResetPassword)
		authGroup.GET("/verify", authHandler.VerifyEmail)
		authGroup.GET("/oauth/:provider", authHandler.OAuthLogin)
		authGroup.GET("/oauth/:provider/callback", authHandler.OAuthCallback)
	}

	// Profile
	profileGroup := r.Group("/profile")
	profileGroup.Use(middleware.AuthRequired(authService))
	{
		profileGroup.GET("/", profileHandler.Show)
		profileGroup.POST("/avatar", profileHandler.UploadAvatar)
		profileGroup.POST("/update", profileHandler.UpdateProfile)
		profileGroup.POST("/change-password", profileHandler.ChangePassword)
	}

	// Achievements
	achievementGroup := r.Group("/achievements")
	achievementGroup.Use(middleware.AuthRequired(authService))
	{
		achievementGroup.GET("/", achievementHandler.List)
	}

	// Dashboard
	dashboardGroup := r.Group("/dashboard")
	dashboardGroup.Use(middleware.AuthRequired(authService))
	{
		dashboardGroup.GET("/", dashboardHandler.Index)
	}

	// Game
	game.RegisterRoutes(r, gameSvc, coAuthorSvc, attemptSvc, progressSvc, monitorSvc, localStorage, hub, cfg, auditSvc, authService)

	// Level
	level.RegisterRoutes(r, levelSvc, questionSvc, answerSvc, localStorage, hub, cfg, coAuthorSvc, gameSvc, authService)

	// Team
	team.RegisterRoutes(r, teamSvc, invitationSvc, cfg, localStorage, coAuthorSvc, authService)

	// Tournament
	tournament.RegisterRoutes(r, tournamentSvc, cfg, authService)

	// Calendar
	calendar.RegisterRoutes(r, gameRepo)

	// Monitor
	monitor.RegisterRoutes(r, db, hub, cfg, coAuthorSvc, monitorSvc, attemptSvc, progressSvc, authService, gameRepo)

	// Social
	social.RegisterRoutes(r, db, cfg, authService)

	// Export
	export.RegisterRoutes(r, db, localStorage, cfg, gameSvc, coAuthorSvc, authService)

	// Gameplay
	gameplayHandler := game.NewGameplayHandler(gameSvc, attemptSvc, progressSvc, monitorSvc, hub, localStorage, db)
	protected := r.Group("/")
	protected.Use(middleware.AuthRequired(authService))
	game.RegisterGameplayRoutes(protected, gameplayHandler, coAuthorSvc)

	return r
}
