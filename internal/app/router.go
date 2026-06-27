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

// =============================================================================
// СТРУКТУРА APP — ВСЕ ЗАВИСИМОСТИ В ОДНОМ МЕСТЕ
// =============================================================================

// App инкапсулирует все зависимости приложения.
type App struct {
	Config       *config.Config
	DB           *gorm.DB
	LocalStorage storage.FileStorage
	Hub          *ws.RoomHub
	BaseDir      string

	Repos    *repositories
	Services *services
	AuditSvc *audit.Service
}

// NewApp создаёт экземпляр App с инициализированными репозиториями и сервисами.
func NewApp(db *gorm.DB, localStorage storage.FileStorage, hub *ws.RoomHub, cfg *config.Config, baseDir string) *App {
	repos := initRepositories(db)
	services := initServices(db, repos, cfg, hub)
	auditSvc := audit.NewService(db)

	return &App{
		Config:       cfg,
		DB:           db,
		LocalStorage: localStorage,
		Hub:          hub,
		BaseDir:      baseDir,
		Repos:        repos,
		Services:     services,
		AuditSvc:     auditSvc,
	}
}

// SetupRouter — главная точка сборки роутера.
// Теперь возвращает ошибку, если не удалось инициализировать какие-либо маршруты.
func (app *App) SetupRouter() (*gin.Engine, error) {
	r := gin.New()

	app.setupEngine(r)
	if err := app.registerAllRoutes(r); err != nil {
		return nil, err
	}

	return r, nil
}

// =============================================================================
// НАСТРОЙКА ДВИЖКА (использует поля App)
// =============================================================================

func (app *App) setupEngine(r *gin.Engine) {
	store := cookie.NewStore([]byte(app.Config.Session.Secret))
	r.Use(gin.Recovery())
	r.Use(sessions.Sessions("gengine_session", store))

	r.Use(csrf.Middleware(csrf.Options{
		Secret: app.Config.Session.Secret,
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
	r.LoadHTMLGlob(filepath.Join(app.BaseDir, "internal", "domain", "*", "templates", "*.html"))

	r.Use(middleware.ContextTimeout(30 * time.Second))
	r.Use(middleware.SecurityHeadersMiddleware())
	r.Use(middleware.GzipMiddleware())
	r.Use(middleware.StaticCacheMiddleware())

	r.Static("/static", filepath.Join(app.BaseDir, "static"))
	r.Static("/uploads", filepath.Join(app.BaseDir, "uploads"))

	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
}

// =============================================================================
// РЕГИСТРАЦИЯ ВСЕХ МАРШРУТОВ (использует поля App)
// =============================================================================

func (app *App) registerAllRoutes(r *gin.Engine) error {
	// Раздаём маршруты по доменам, передавая только нужные зависимости.
	app.registerAdminRoutes(r)
	app.registerUserRoutes(r)
	app.registerGameRoutes(r)
	app.registerLevelRoutes(r)
	app.registerTeamRoutes(r)
	app.registerTournamentRoutes(r)
	app.registerCalendarRoutes(r)
	app.registerMonitorRoutes(r)
	app.registerSocialRoutes(r)
	if err := app.registerExportRoutes(r); err != nil {
		return fmt.Errorf("регистрация маршрутов экспорта: %w", err)
	}
	app.registerGameplayRoutes(r)

	return nil
}

// =============================================================================
// ИНИЦИАЛИЗАЦИЯ РЕПОЗИТОРИЕВ (без изменений)
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
// ИНИЦИАЛИЗАЦИЯ СЕРВИСОВ (без изменений)
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
// РЕГИСТРАЦИЯ МАРШРУТОВ (каждая функция принимает только то, что нужно)
// =============================================================================

func (app *App) registerAdminRoutes(r *gin.Engine) {
	admin.RegisterRoutes(r, app.DB, app.Config, app.Services.Auth, app.Repos.User, app.Repos.Game)
}

func (app *App) registerUserRoutes(r *gin.Engine) {
	// Создаём обработчики только для user-домена.
	authHandler := user.NewAuthHandler(
		app.Config,
		app.Services.Auth,
		app.Services.User,
		app.Services.PasswordReset,
		app.Services.EmailVerif,
		app.Services.OAuth,
		app.AuditSvc,
	)
	profileHandler := user.NewProfileHandler(app.DB, app.LocalStorage)
	achievementHandler := user.NewAchievementHandler(app.DB)
	dashboardHandler := user.NewDashboardHandler(user.NewUserDashboardService(app.DB), app.DB)

	// Для OAuth эндпоинтов используем тот же лимитер, что и для обычного входа,
	// чтобы защититься от брутфорса через внешние провайдеры.
	oauthRateLimit := middleware.LoginRateLimit(5*time.Minute, 5)

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
		authGroup.GET("/oauth/:provider", oauthRateLimit, authHandler.OAuthLogin)
		authGroup.GET("/oauth/:provider/callback", oauthRateLimit, authHandler.OAuthCallback)
	}

	profileGroup := r.Group("/profile")
	profileGroup.Use(middleware.AuthRequired(app.Services.Auth))
	{
		profileGroup.GET("/", profileHandler.Show)
		profileGroup.POST("/avatar", profileHandler.UploadAvatar)
		profileGroup.POST("/update", profileHandler.UpdateProfile)
		profileGroup.POST("/change-password", profileHandler.ChangePassword)
	}

	achievementGroup := r.Group("/achievements")
	achievementGroup.Use(middleware.AuthRequired(app.Services.Auth))
	{
		achievementGroup.GET("/", achievementHandler.List)
	}

	dashboardGroup := r.Group("/dashboard")
	dashboardGroup.Use(middleware.AuthRequired(app.Services.Auth))
	{
		dashboardGroup.GET("/", dashboardHandler.Index)
	}
}

func (app *App) registerGameRoutes(r *gin.Engine) {
	game.RegisterRoutes(
		r,
		app.Services.Game,
		app.Services.CoAuthor,
		app.Services.Attempt,
		app.Services.Progress,
		app.Services.Monitor,
		app.LocalStorage,
		app.Hub,
		app.Config,
		app.AuditSvc,
		app.Services.Auth,
	)
}

func (app *App) registerLevelRoutes(r *gin.Engine) {
	level.RegisterRoutes(
		r,
		app.Services.Level,
		app.Services.Question,
		app.Services.Answer,
		app.LocalStorage,
		app.Hub,
		app.Config,
		app.Services.CoAuthor,
		app.Services.Game,
		app.Services.Auth,
	)
}

func (app *App) registerTeamRoutes(r *gin.Engine) {
	team.RegisterRoutes(
		r,
		app.Services.Team,
		app.Services.Invitation,
		app.Config,
		app.LocalStorage,
		app.Services.CoAuthor,
		app.Services.Auth,
	)
}

func (app *App) registerTournamentRoutes(r *gin.Engine) {
	tournament.RegisterRoutes(r, app.Services.Tournament, app.Config, app.Services.Auth)
}

func (app *App) registerCalendarRoutes(r *gin.Engine) {
	calendar.RegisterRoutes(r, app.Repos.Game)
}

func (app *App) registerMonitorRoutes(r *gin.Engine) {
	monitor.RegisterRoutes(
		r,
		app.DB,
		app.Hub,
		app.Config,
		app.Services.CoAuthor,
		app.Services.Monitor,
		app.Services.Attempt,
		app.Services.Progress,
		app.Services.Auth,
		app.Repos.Game,
	)
}

func (app *App) registerSocialRoutes(r *gin.Engine) {
	social.RegisterRoutes(r, app.DB, app.Config, app.Services.Auth)
}

// +++ Изменяем сигнатуру на возврат ошибки
func (app *App) registerExportRoutes(r *gin.Engine) error {
	return export.RegisterRoutes(
		r,
		app.DB,
		app.LocalStorage,
		app.Config,
		app.Services.Game,
		app.Services.CoAuthor,
		app.Services.Auth,
	)
}

func (app *App) registerGameplayRoutes(r *gin.Engine) {
	gameplayHandler := game.NewGameplayHandler(
		app.Services.Game,
		app.Services.Attempt,
		app.Services.Progress,
		app.Services.Monitor,
		app.Hub,
		app.LocalStorage,
		app.DB,
	)
	protected := r.Group("/")
	protected.Use(middleware.AuthRequired(app.Services.Auth))
	game.RegisterGameplayRoutes(protected, gameplayHandler, app.Services.CoAuthor)
}

// =============================================================================
// ОБРАТНАЯ СОВМЕСТИМОСТЬ: оставляем старую функцию SetupRouter для упрощения перехода
// =============================================================================

// SetupRouter — сохранена для обратной совместимости, но теперь возвращает ошибку.
// Рекомендуется использовать App.SetupRouter().
func SetupRouter(db *gorm.DB, localStorage storage.FileStorage, hub *ws.RoomHub, cfg *config.Config, baseDir string) (*gin.Engine, error) {
	app := NewApp(db, localStorage, hub, cfg, baseDir)
	return app.SetupRouter()
}
