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
	"gengine-0/internal/pkg/audit"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	_ "gengine-0/docs" // Swagger-документация (генерируется автоматически)

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	csrf "github.com/utrack/gin-csrf"
	"gorm.io/gorm"
)

// SetupRouter — главная точка сборки роутера.
func SetupRouter(db *gorm.DB, localStorage storage.FileStorage, hub *ws.RoomHub, cfg *config.Config, baseDir string) *gin.Engine {
	r := gin.New()

	setupEngine(r, cfg, baseDir)
	repos := initRepositories(db)
	services := initServices(db, repos, cfg, hub) // убрали localStorage

	auditSvc := registerAdminRoutes(r, db, cfg, services.Auth, repos.User, repos.Game)
	registerUserRoutes(r, cfg, services, auditSvc, db, localStorage)
	registerGameRoutes(r, cfg, services, localStorage, hub, auditSvc)
	registerLevelRoutes(r, cfg, services, localStorage, hub)
	registerTeamRoutes(r, cfg, services, localStorage)
	registerTournamentRoutes(r, cfg, services)
	registerCalendarRoutes(r, repos.Game)
	registerMonitorRoutes(r, db, cfg, services, hub, repos.Game)
	registerSocialRoutes(r, db, cfg, services.Auth)
	registerExportRoutes(r, db, localStorage, cfg, services)
	registerGameplayRoutes(r, services, localStorage, hub, db)

	return r
}

// =============================================================================
// НАСТРОЙКА ДВИЖКА
// =============================================================================

func setupEngine(r *gin.Engine, cfg *config.Config, baseDir string) {
	store := cookie.NewStore([]byte(cfg.Session.Secret))
	r.Use(gin.Recovery())
	r.Use(sessions.Sessions("gengine_session", store))

	r.Use(csrf.Middleware(csrf.Options{
		Secret: cfg.Session.Secret,
		ErrorFunc: func(c *gin.Context) {
			c.String(403, "CSRF token mismatch")
			c.Abort()
		},
	}))

	// Функции для шаблонов
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

	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
}

// =============================================================================
// ИНИЦИАЛИЗАЦИЯ РЕПОЗИТОРИЕВ (ВСЕ — ИНТЕРФЕЙСЫ)
// =============================================================================

type repositories struct {
	User        user.UserRepository
	Achiev      user.AchievementRepository
	PassReset   user.PasswordResetRepository
	EmailVerif  user.EmailVerificationRepository
	ExtLogin    user.ExternalLoginRepository
	Game        game.GameRepository
	GamePassing game.GamePassingRepository
	Level       level.LevelRepository
	Question    level.QuestionRepository
	Answer      level.AnswerRepository
	Team        team.TeamRepository
	Invitation  team.InvitationRepository
	Tournament  tournament.TournamentRepository
	TournGame   tournament.TournamentGameRepository
	TournTeam   tournament.TournamentTeamRepository
	TournResult tournament.TournamentResultRepository
}

func initRepositories(db *gorm.DB) *repositories {
	return &repositories{
		User:        user.NewGormUserRepo(db),
		Achiev:      user.NewGormAchievementRepo(db),
		PassReset:   user.NewGormPasswordResetRepo(db),
		EmailVerif:  user.NewGormEmailVerificationRepo(db),
		ExtLogin:    user.NewGormExternalLoginRepo(db),
		Game:        game.NewGormGameRepo(db),
		GamePassing: game.NewGormGamePassingRepo(db),
		Level:       level.NewGormLevelRepo(db),
		Question:    level.NewGormQuestionRepo(db),
		Answer:      level.NewGormAnswerRepo(db),
		Team:        team.NewGormTeamRepo(db),
		Invitation:  team.NewGormInvitationRepo(db),
		Tournament:  tournament.NewGormTournamentRepo(db),
		TournGame:   tournament.NewGormTournamentGameRepo(db),
		TournTeam:   tournament.NewGormTournamentTeamRepo(db),
		TournResult: tournament.NewGormTournamentResultRepo(db),
	}
}

// =============================================================================
// ИНИЦИАЛИЗАЦИЯ СЕРВИСОВ
// =============================================================================

type services struct {
	Auth          *user.AuthService
	User          *user.UserService
	Achiev        *user.AchievementService
	OAuth         *user.OAuthService
	PasswordReset *user.PasswordResetService
	EmailVerif    *user.EmailVerificationService
	Game          *game.GameService
	CoAuthor      *game.CoAuthorService
	Review        *game.ReviewService
	Attempt       *game.AttemptService
	Progress      *game.LevelProgressService
	Monitor       *game.MonitorService
	Level         *level.LevelService
	Question      *level.QuestionService
	Answer        *level.AnswerService
	Team          *team.TeamService
	Invitation    *team.InvitationService
	Tournament    *tournament.TournamentService
}

func initServices(db *gorm.DB, repos *repositories, cfg *config.Config, hub *ws.RoomHub) *services {
	coAuthorSvc := game.NewCoAuthorService(db)
	reviewSvc := game.NewReviewService(db)
	attemptSvc := game.NewAttemptService(db)
	progressSvc := game.NewLevelProgressService(db)
	monitorSvc := game.NewMonitorService(db)

	authSvc := user.NewAuthService(repos.User, repos.Achiev, repos.EmailVerif, cfg)
	userSvc := user.NewUserService(repos.User)
	achievSvc := user.NewAchievementService(repos.Achiev)
	oauthSvc := user.NewOAuthService(repos.User, repos.ExtLogin, cfg)
	passResetSvc := user.NewPasswordResetService(repos.User, repos.PassReset, cfg)
	emailVerifSvc := user.NewEmailVerificationService(repos.User, repos.EmailVerif, cfg)

	gameSvc := game.NewGameService(
		repos.Game,
		repos.GamePassing,
		coAuthorSvc,
		reviewSvc,
		monitorSvc,
		hub,
		attemptSvc,
		progressSvc,
		cfg,
	)

	levelSvc := level.NewLevelService(repos.Level, repos.Question, repos.Answer, coAuthorSvc, gameSvc)
	questionSvc := level.NewQuestionService(repos.Question, repos.Level, coAuthorSvc)
	answerSvc := level.NewAnswerService(repos.Answer, repos.Question, repos.Level, coAuthorSvc)

	teamSvc := team.NewTeamService(repos.Team, coAuthorSvc)
	invitationSvc := team.NewInvitationService(repos.Invitation, repos.Team, coAuthorSvc, cfg)

	tournamentSvc := tournament.NewTournamentService(
		repos.Tournament,
		repos.TournGame,
		repos.TournTeam,
		repos.TournResult,
		teamSvc,
		cfg,
	)

	return &services{
		Auth:          authSvc,
		User:          userSvc,
		Achiev:        achievSvc,
		OAuth:         oauthSvc,
		PasswordReset: passResetSvc,
		EmailVerif:    emailVerifSvc,
		Game:          gameSvc,
		CoAuthor:      coAuthorSvc,
		Review:        reviewSvc,
		Attempt:       attemptSvc,
		Progress:      progressSvc,
		Monitor:       monitorSvc,
		Level:         levelSvc,
		Question:      questionSvc,
		Answer:        answerSvc,
		Team:          teamSvc,
		Invitation:    invitationSvc,
		Tournament:    tournamentSvc,
	}
}

// =============================================================================
// РЕГИСТРАЦИЯ МАРШРУТОВ (ФУНКЦИИ ПРИНИМАЮТ ИНТЕРФЕЙСЫ)
// =============================================================================

func registerAdminRoutes(r *gin.Engine, db *gorm.DB, cfg *config.Config, authSvc *user.AuthService, userRepo user.UserRepository, gameRepo game.GameRepository) *audit.Service {
	return admin.RegisterRoutes(r, db, cfg, authSvc, userRepo, gameRepo)
}

func registerUserRoutes(r *gin.Engine, cfg *config.Config, svc *services, auditSvc *audit.Service, db *gorm.DB, localStorage storage.FileStorage) {
	authHandler := user.NewAuthHandler(cfg, svc.Auth, svc.User, svc.PasswordReset, svc.EmailVerif, svc.OAuth, auditSvc)
	profileHandler := user.NewProfileHandler(db, localStorage)
	achievementHandler := user.NewAchievementHandler(db)
	dashboardHandler := user.NewDashboardHandler(user.NewUserDashboardService(db), db)

	authGroup := r.Group("/auth")
	{
		authGroup.GET("/login", authHandler.ShowLoginForm)
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

	profileGroup := r.Group("/profile")
	profileGroup.Use(middleware.AuthRequired(svc.Auth))
	{
		profileGroup.GET("/", profileHandler.Show)
		profileGroup.POST("/avatar", profileHandler.UploadAvatar)
		profileGroup.POST("/update", profileHandler.UpdateProfile)
		profileGroup.POST("/change-password", profileHandler.ChangePassword)
	}

	achievementGroup := r.Group("/achievements")
	achievementGroup.Use(middleware.AuthRequired(svc.Auth))
	{
		achievementGroup.GET("/", achievementHandler.List)
	}

	dashboardGroup := r.Group("/dashboard")
	dashboardGroup.Use(middleware.AuthRequired(svc.Auth))
	{
		dashboardGroup.GET("/", dashboardHandler.Index)
	}
}

func registerGameRoutes(r *gin.Engine, cfg *config.Config, svc *services, localStorage storage.FileStorage, hub *ws.RoomHub, auditSvc *audit.Service) {
	game.RegisterRoutes(r, svc.Game, svc.CoAuthor, svc.Attempt, svc.Progress, svc.Monitor, localStorage, hub, cfg, auditSvc, svc.Auth)
}

func registerLevelRoutes(r *gin.Engine, cfg *config.Config, svc *services, localStorage storage.FileStorage, hub *ws.RoomHub) {
	level.RegisterRoutes(r, svc.Level, svc.Question, svc.Answer, localStorage, hub, cfg, svc.CoAuthor, svc.Game, svc.Auth)
}

func registerTeamRoutes(r *gin.Engine, cfg *config.Config, svc *services, localStorage storage.FileStorage) {
	team.RegisterRoutes(r, svc.Team, svc.Invitation, cfg, localStorage, svc.CoAuthor, svc.Auth)
}

func registerTournamentRoutes(r *gin.Engine, cfg *config.Config, svc *services) {
	tournament.RegisterRoutes(r, svc.Tournament, cfg, svc.Auth)
}

func registerCalendarRoutes(r *gin.Engine, gameRepo game.GameRepository) {
	calendar.RegisterRoutes(r, gameRepo)
}

func registerMonitorRoutes(r *gin.Engine, db *gorm.DB, cfg *config.Config, svc *services, hub *ws.RoomHub, gameRepo game.GameRepository) {
	monitor.RegisterRoutes(r, db, hub, cfg, svc.CoAuthor, svc.Monitor, svc.Attempt, svc.Progress, svc.Auth, gameRepo)
}

func registerSocialRoutes(r *gin.Engine, db *gorm.DB, cfg *config.Config, authSvc *user.AuthService) {
	social.RegisterRoutes(r, db, cfg, authSvc)
}

func registerExportRoutes(r *gin.Engine, db *gorm.DB, localStorage storage.FileStorage, cfg *config.Config, svc *services) {
	export.RegisterRoutes(r, db, localStorage, cfg, svc.Game, svc.CoAuthor, svc.Auth)
}

func registerGameplayRoutes(r *gin.Engine, svc *services, localStorage storage.FileStorage, hub *ws.RoomHub, db *gorm.DB) {
	gameplayHandler := game.NewGameplayHandler(svc.Game, svc.Attempt, svc.Progress, svc.Monitor, hub, localStorage, db)
	protected := r.Group("/")
	protected.Use(middleware.AuthRequired(svc.Auth))
	game.RegisterGameplayRoutes(protected, gameplayHandler, svc.CoAuthor)
}
