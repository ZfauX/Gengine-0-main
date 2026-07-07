// internal/app/router.go
package app

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/admin"
	"gengine-0/internal/domain/calendar"
	"gengine-0/internal/domain/export"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/monitor"
	"gengine-0/internal/domain/notification"
	"gengine-0/internal/domain/social"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/tournament"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/audit"
	"gengine-0/internal/pkg/cache"
	"gengine-0/internal/pkg/health"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/storage"
	"gengine-0/internal/pkg/templatefuncs"
	ws "gengine-0/internal/pkg/websocket"

	_ "gengine-0/docs"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	csrf "github.com/utrack/gin-csrf"
	"gorm.io/gorm"
)

// =============================================================================
// ЗАВИСИМОСТИ — ВСЕ КОМПОНЕНТЫ ВЫНЕСЕНЫ В ОТДЕЛЬНУЮ СТРУКТУРУ
// =============================================================================

type Dependencies struct {
	Repos    *repositories
	Services *services
	AuditSvc *audit.Service
}

func NewDependencies(db *gorm.DB, cfg *config.Config, hub *ws.RoomHub, localStorage storage.FileStorage, appCache *cache.Cache) *Dependencies {
	repos := initRepositories(db)
	services := initServices(db, repos, cfg, hub, localStorage, appCache)
	auditSvc := audit.NewService(db)

	return &Dependencies{
		Repos:    repos,
		Services: services,
		AuditSvc: auditSvc,
	}
}

// =============================================================================
// СТРУКТУРА APP — ТОЛЬКО КОМПОНЕНТЫ, НЕОБХОДИМЫЕ ДЛЯ НАСТРОЙКИ
// =============================================================================

type App struct {
	Config       *config.Config
	DB           *gorm.DB
	LocalStorage storage.FileStorage
	Hub          *ws.RoomHub
	BaseDir      string

	Deps *Dependencies
}

func NewApp(
	db *gorm.DB,
	localStorage storage.FileStorage,
	hub *ws.RoomHub,
	cfg *config.Config,
	baseDir string,
	deps *Dependencies,
) *App {
	return &App{
		Config:       cfg,
		DB:           db,
		LocalStorage: localStorage,
		Hub:          hub,
		BaseDir:      baseDir,
		Deps:         deps,
	}
}

func (app *App) SetupRouter() (*gin.Engine, error) {
	r := gin.New()

	app.setupEngine(r)
	if err := app.registerAllRoutes(r); err != nil {
		return nil, err
	}

	return r, nil
}

// =============================================================================
// НАСТРОЙКА ДВИЖКА
// =============================================================================

func (app *App) setupEngine(r *gin.Engine) {
	store := cookie.NewStore([]byte(app.Config.Session.Secret))
	r.Use(gin.Recovery())
	r.Use(middleware.ErrorHandler())
	r.Use(middleware.LoggerMiddleware())
	r.Use(sessions.Sessions("gengine_session", store))

	r.Use(csrf.Middleware(csrf.Options{
		Secret: app.Config.Session.Secret,
		ErrorFunc: func(c *gin.Context) {
			c.String(403, "CSRF token mismatch")
			c.Abort()
		},
	}))

	tmpl := template.New("")
	tmpl.Funcs(templatefuncs.FuncMap())
	_, err := tmpl.ParseGlob(filepath.Join(app.BaseDir, "internal", "domain", "*", "templates", "*.html"))
	if err != nil {
		log.Fatal().Err(err).Msg("Не удалось загрузить шаблоны")
	}
	r.SetHTMLTemplate(tmpl)
	render.SetTemplate(tmpl)

	r.Use(middleware.ContextTimeout(30 * time.Second))
	r.Use(middleware.SecurityHeadersMiddleware())
	r.Use(middleware.GzipMiddleware())
	r.Use(middleware.StaticCacheMiddleware())

	r.Static("/static", filepath.Join(app.BaseDir, "static"))
	r.Static("/uploads", filepath.Join(app.BaseDir, "uploads"))

	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	healthChecker := health.NewChecker(app.DB, app.Hub)
	r.GET("/healthz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		resp := healthChecker.Check(ctx)

		var statusCode int
		switch resp.Status {
		case "error":
			statusCode = http.StatusServiceUnavailable
		case "degraded":
			statusCode = http.StatusMultiStatus
		default:
			statusCode = http.StatusOK
		}
		c.JSON(statusCode, resp)
	})

	// ============================================================
	// ГЛАВНАЯ СТРАНИЦА — используем OptionalAuth вместо дублирования логики
	// ============================================================
	r.GET("/", middleware.OptionalAuth(app.Deps.Services.Auth), func(c *gin.Context) {
		var userID uint
		var role string
		if id, ok := c.Get("userID"); ok {
			userID = id.(uint)
		}
		if r, ok := c.Get("role"); ok {
			role = r.(string)
		}
		render.Page(c, http.StatusOK, "home.html", gin.H{
			"CurrentUserID": userID,
			"IsAdmin":       role == "admin",
			"csrf":          csrf.GetToken(c),
		})
	})
}

// =============================================================================
// РЕГИСТРАЦИЯ ВСЕХ МАРШРУТОВ
// =============================================================================

func (app *App) registerAllRoutes(r *gin.Engine) error {
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
	notification.RegisterRoutes(r, app.DB, app.Deps.Services.Auth)
	return nil
}

// =============================================================================
// ИНИЦИАЛИЗАЦИЯ РЕПОЗИТОРИЕВ
// =============================================================================

type repositories struct {
	User         user.UserRepository
	Achiev       user.AchievementRepository
	PassReset    user.PasswordResetRepository
	EmailVerif   user.EmailVerificationRepository
	ExtLogin     user.ExternalLoginRepository
	RefreshToken user.RefreshTokenRepository
	Game         game.GameRepository
	GamePassing  game.GamePassingRepository
	Level        level.LevelRepository
	Question     level.QuestionRepository
	Answer       level.AnswerRepository
	Team         team.TeamRepository
	Invitation   team.InvitationRepository
	Tournament   tournament.TournamentRepository
	TournGame    tournament.TournamentGameRepository
	TournTeam    tournament.TournamentTeamRepository
	TournResult  tournament.TournamentResultRepository
}

func initRepositories(db *gorm.DB) *repositories {
	return &repositories{
		User:         user.NewGormUserRepo(db),
		Achiev:       user.NewGormAchievementRepo(db),
		PassReset:    user.NewGormPasswordResetRepo(db),
		EmailVerif:   user.NewGormEmailVerificationRepo(db),
		ExtLogin:     user.NewGormExternalLoginRepo(db),
		RefreshToken: user.NewGormRefreshTokenRepo(db),
		Game:         game.NewGormGameRepo(db),
		GamePassing:  game.NewGormGamePassingRepo(db),
		Level:        level.NewGormLevelRepo(db),
		Question:     level.NewGormQuestionRepo(db),
		Answer:       level.NewGormAnswerRepo(db),
		Team:         team.NewGormTeamRepo(db),
		Invitation:   team.NewGormInvitationRepo(db),
		Tournament:   tournament.NewGormTournamentRepo(db),
		TournGame:    tournament.NewGormTournamentGameRepo(db),
		TournTeam:    tournament.NewGormTournamentTeamRepo(db),
		TournResult:  tournament.NewGormTournamentResultRepo(db),
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
	GamePlay      *game.GamePlayService
	GameAdmin     *game.GameAdminService
	CoAuthor      *game.CoAuthorService
	Review        *game.ReviewService
	Attempt       *game.AttemptService
	Progress      *game.LevelProgressService
	Monitor       *game.MonitorService
	Rating        *game.RatingService
	Level         *level.LevelService
	Question      *level.QuestionService
	Answer        *level.AnswerService
	Team          *team.TeamService
	Invitation    *team.InvitationService
	Tournament    *tournament.TournamentService
}

func initServices(db *gorm.DB, repos *repositories, cfg *config.Config, hub *ws.RoomHub, localStorage storage.FileStorage, appCache *cache.Cache) *services {
	coAuthorSvc := game.NewCoAuthorService(db)
	reviewSvc := game.NewReviewService(db)
	attemptSvc := game.NewAttemptService(db)
	progressSvc := game.NewLevelProgressService(db)
	monitorSvc := game.NewMonitorService(db)
	ratingSvc := game.NewRatingService(db, appCache)

	authSvc := user.NewAuthService(
		repos.User,
		repos.Achiev,
		repos.EmailVerif,
		repos.RefreshToken,
		cfg,
	)
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
		cfg,
		localStorage,
		appCache,
		repos.User,
	)

	gamePlaySvc := game.NewGamePlayService(
		db,
		attemptSvc,
		progressSvc,
		monitorSvc,
		hub,
		coAuthorSvc,
		cfg,
	)

	gameAdminSvc := game.NewGameAdminService(
		db,
		coAuthorSvc,
		cfg,
	)

	levelSvc := level.NewLevelService(repos.Level, repos.Question, repos.Answer, coAuthorSvc, gameAdminSvc)
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
		GamePlay:      gamePlaySvc,
		GameAdmin:     gameAdminSvc,
		CoAuthor:      coAuthorSvc,
		Review:        reviewSvc,
		Attempt:       attemptSvc,
		Progress:      progressSvc,
		Monitor:       monitorSvc,
		Rating:        ratingSvc,
		Level:         levelSvc,
		Question:      questionSvc,
		Answer:        answerSvc,
		Team:          teamSvc,
		Invitation:    invitationSvc,
		Tournament:    tournamentSvc,
	}
}

// =============================================================================
// РЕГИСТРАЦИЯ МАРШРУТОВ — ИСПОЛЬЗУЮТ ЗАВИСИМОСТИ ИЗ App.Deps
// =============================================================================

func (app *App) registerAdminRoutes(r *gin.Engine) {
	_ = admin.RegisterRoutes(r, app.DB, app.Config, app.Deps.Services.Auth, app.Deps.Repos.User, app.Deps.Repos.Game)
}

func (app *App) registerUserRoutes(r *gin.Engine) {
	user.RegisterRoutes(
		r,
		app.Config,
		app.Deps.Services.Auth,
		app.Deps.Services.User,
		app.Deps.Services.PasswordReset,
		app.Deps.Services.EmailVerif,
		app.Deps.Services.OAuth,
		app.Deps.AuditSvc,
		app.DB,
		app.LocalStorage,
	)
}

func (app *App) registerGameRoutes(r *gin.Engine) {
	passingSvc := game.NewGamePassingService(app.DB, app.Deps.Services.Team, app.Deps.Services.CoAuthor)
	game.RegisterRoutes(
		r,
		app.DB,
		app.Deps.Services.Game,
		passingSvc,
		app.Deps.Services.CoAuthor,
		app.Deps.Services.Attempt,
		app.Deps.Services.Progress,
		app.Deps.Services.Monitor,
		app.LocalStorage,
		app.Hub,
		app.Config,
		app.Deps.AuditSvc,
		app.Deps.Services.Auth,
		app.Deps.Services.GamePlay,
		app.Deps.Services.GameAdmin,
		app.Deps.Services.Review, // передаём ReviewService
	)
}

func (app *App) registerLevelRoutes(r *gin.Engine) {
	level.RegisterRoutes(
		r,
		app.Deps.Services.Level,
		app.Deps.Services.Question,
		app.Deps.Services.Answer,
		app.LocalStorage,
		app.Hub,
		app.Config,
		app.Deps.Services.CoAuthor,
		app.Deps.Services.Auth,
	)
}

func (app *App) registerTeamRoutes(r *gin.Engine) {
	team.RegisterRoutes(
		r,
		app.Deps.Services.Team,
		app.Deps.Services.Invitation,
		app.Config,
		app.LocalStorage,
		app.Deps.Services.CoAuthor,
		app.Deps.Services.Auth,
	)
}

func (app *App) registerTournamentRoutes(r *gin.Engine) {
	tournament.RegisterRoutes(r, app.Deps.Services.Tournament, app.Deps.Services.Team, app.Config, app.Deps.Services.Auth)
}

func (app *App) registerCalendarRoutes(r *gin.Engine) {
	calendar.RegisterRoutes(r, app.Deps.Repos.Game)
}

func (app *App) registerMonitorRoutes(r *gin.Engine) {
	monitor.RegisterRoutes(
		r,
		app.DB,
		app.Hub,
		app.Config,
		app.Deps.Services.CoAuthor,
		app.Deps.Services.Monitor,
		app.Deps.Services.Attempt,
		app.Deps.Services.Progress,
		app.Deps.Services.Auth,
		app.Deps.Repos.Game,
	)
}

func (app *App) registerSocialRoutes(r *gin.Engine) {
	social.RegisterRoutes(r, app.DB, app.Config, app.Deps.Services.Auth)
}

func (app *App) registerExportRoutes(r *gin.Engine) error {
	return export.RegisterRoutes(
		r,
		app.DB,
		app.LocalStorage,
		app.Config,
		app.Deps.Services.Game,
		app.Deps.Services.CoAuthor,
		app.Deps.Services.Auth,
	)
}

func (app *App) registerGameplayRoutes(r *gin.Engine) {
	gameplayHandler := game.NewGameplayHandler(
		app.Deps.Services.Game,
		app.Deps.Services.GamePlay,
		app.Deps.Services.Attempt,
		app.Deps.Services.Progress,
		app.Deps.Services.Monitor,
		app.Hub,
		app.LocalStorage,
		app.DB,
	)
	protected := r.Group("/")
	protected.Use(middleware.AuthRequired(app.Deps.Services.Auth))
	game.RegisterGameplayRoutes(protected, gameplayHandler, app.Deps.Services.CoAuthor)
}

// =============================================================================
// ВСПОМОГАТЕЛЬНАЯ ФУНКЦИЯ ДЛЯ ОБРАТНОЙ СОВМЕСТИМОСТИ
// =============================================================================

// SetupRouter — legacy-функция для обратной совместимости с тестами.
// Создаёт новый router с нуля (deps создаются внутри).
func SetupRouter(db *gorm.DB, localStorage storage.FileStorage, hub *ws.RoomHub, cfg *config.Config, baseDir string, appCache *cache.Cache) (*gin.Engine, error) {
	deps := NewDependencies(db, cfg, hub, localStorage, appCache)
	app := NewApp(db, localStorage, hub, cfg, baseDir, deps)
	return app.SetupRouter()
}

// SetupRouterWithDeps — альтернатива, которая принимает готовые deps (избегает дублирования).
func SetupRouterWithDeps(db *gorm.DB, localStorage storage.FileStorage, hub *ws.RoomHub, cfg *config.Config, baseDir string, deps *Dependencies) (*gin.Engine, error) {
	app := NewApp(db, localStorage, hub, cfg, baseDir, deps)
	return app.SetupRouter()
}
