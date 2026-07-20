// internal/app/router.go
package app

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"strings"
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
	Cache    cache.CacheStore
	Hub      *ws.RoomHub
}

func NewDependencies(db *gorm.DB, cfg *config.Config, hub *ws.RoomHub, localStorage storage.FileStorage, appCache cache.CacheStore) *Dependencies {
	repos := initRepositories(db)
	services := initServices(db, repos, cfg, hub, localStorage, appCache)
	auditSvc := audit.NewService(db)

	return &Dependencies{
		Repos:    repos,
		Services: services,
		AuditSvc: auditSvc,
		Cache:    appCache,
		Hub:      hub,
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

	if err := app.setupEngine(r); err != nil {
		return nil, err
	}

	// Создаём группу для HTML-маршрутов с CSRF-защитой
	htmlGroup := r.Group("")
	htmlGroup.Use(csrf.Middleware(csrf.Options{
		Secret: app.Config.Session.Secret,
		ErrorFunc: func(c *gin.Context) {
			c.String(403, "CSRF token mismatch")
			c.Abort()
		},
	}))

	if err := app.registerAllRoutes(r, htmlGroup); err != nil {
		return nil, err
	}

	return r, nil
}

// =============================================================================
// НАСТРОЙКА ДВИЖКА
// =============================================================================

func (app *App) setupEngine(r *gin.Engine) error {
	store := cookie.NewStore([]byte(app.Config.Session.Secret))
	store.Options(sessions.Options{
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	r.Use(gin.Recovery())
	r.Use(middleware.ErrorHandler())
	r.Use(middleware.LoggerMiddleware())
	r.Use(sessions.Sessions("gengine_session", store))

	// API маршруты не требуют CSRF (используют JWT + X-CSRF-Token)
	apiGroup := r.Group("/api")
	apiGroup.Use(middleware.CSRFJSON())

	tmpl := template.New("")
	tmpl.Funcs(templatefuncs.FuncMap())
	_, err := tmpl.ParseGlob(filepath.Join(app.BaseDir, "internal", "domain", "*", "templates", "*.html"))
	if err != nil {
		return fmt.Errorf("не удалось загрузить шаблоны: %w", err)
	}
	r.SetHTMLTemplate(tmpl)
	render.SetTemplate(tmpl)

	r.Use(middleware.ContextTimeout(30 * time.Second))

	// 🔒 Swagger и Metrics доступны только авторизованным пользователям (защита от утечки информации)
	r.GET("/swagger/*any", middleware.OptionalAuth(app.Deps.Services.Auth), func(c *gin.Context) {
		if c.GetUint("userID") == 0 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "доступно только авторизованным пользователям"})
			return
		}
		ginSwagger.WrapHandler(swaggerFiles.Handler)(c)
	})
	r.GET("/metrics", middleware.OptionalAuth(app.Deps.Services.Auth), func(c *gin.Context) {
		if c.GetUint("userID") == 0 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "доступно только авторизованным пользователям"})
			return
		}
		gin.WrapH(promhttp.Handler())(c)
	})

	healthChecker := health.NewCheckerWithValkey(app.DB, app.Hub, app.Deps.Cache)
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

	// SEO: sitemap.xml и robots.txt
	r.GET("/sitemap.xml", func(c *gin.Context) {
		c.XML(http.StatusOK, gin.H{
			"urlset": gin.H{
				"-xmlns": "http://www.sitemaps.org/schemas/sitemap/0.9",
				"url": []gin.H{
					{"loc": app.Config.Server.BaseURL + "/", "changefreq": "daily", "priority": "1.0"},
					{"loc": app.Config.Server.BaseURL + "/games", "changefreq": "hourly", "priority": "0.9"},
					{"loc": app.Config.Server.BaseURL + "/calendar", "changefreq": "daily", "priority": "0.7"},
				},
			},
		})
	})
	r.GET("/robots.txt", func(c *gin.Context) {
		c.String(http.StatusOK, "User-agent: *\nAllow: /\nSitemap: "+app.Config.Server.BaseURL+"/sitemap.xml\n")
	})

	// Offline page for Service Worker
	r.GET("/offline", func(c *gin.Context) {
		render.Page(c, http.StatusOK, "offline.html", gin.H{"Title": "Нет соединения"})
	})

	// Настройка доверенных прокси для корректного определения реальных IP клиентов
	// Явно указываем доверенные прокси, чтобы предотвратить IP-spoofing
	if app.Config.Server.TrustedProxies != "" {
		proxies := strings.Split(app.Config.Server.TrustedProxies, ",")
		trusted := make([]string, 0, len(proxies))
		for _, p := range proxies {
			p = strings.TrimSpace(p)
			if p != "" {
				trusted = append(trusted, p)
			}
		}
		if err := r.SetTrustedProxies(trusted); err != nil {
			return fmt.Errorf("неверные доверенные прокси: %w", err)
		}
	} else {
		// Без явной конфигурации — не доверяем ни одному прокси
		r.SetTrustedProxies(nil)
	}

	r.Static("/static", filepath.Join(app.BaseDir, app.Config.Server.StaticDir))
	r.Static("/uploads", filepath.Join(app.BaseDir, app.Config.Server.UploadsDir))

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

	return nil
}

// =============================================================================
// РЕГИСТРАЦИЯ ВСЕХ МАРШРУТОВ
// =============================================================================

func (app *App) registerAllRoutes(r *gin.Engine, htmlGroup *gin.RouterGroup) error {
	app.registerAdminRoutes(htmlGroup)
	app.registerUserRoutes(htmlGroup)
	app.registerGameRoutes(htmlGroup)
	app.registerLevelRoutes(htmlGroup)
	app.registerTeamRoutes(htmlGroup)
	app.registerTournamentRoutes(htmlGroup)
	app.registerCalendarRoutes(htmlGroup)
	app.registerMonitorRoutes(htmlGroup)
	app.registerSocialRoutes(htmlGroup)
	if err := app.registerExportRoutes(htmlGroup); err != nil {
		return fmt.Errorf("регистрация маршрутов экспорта: %w", err)
	}
	app.registerGameplayRoutes(htmlGroup)
	notification.RegisterRoutes(htmlGroup, app.DB, app.Deps.Services.Auth, app.Deps.Hub)
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
	Auth            *user.AuthService
	User            *user.UserService
	Achiev          *user.AchievementService
	OAuth           *user.OAuthService
	PasswordReset   *user.PasswordResetService
	EmailVerif      *user.EmailVerificationService
	Game            *game.GameService
	GamePlay        *game.GamePlayService
	GameAdmin       *game.GameAdminService
	GameplayHandler *game.GameplayHandler
	CoAuthor        *game.CoAuthorService
	Review          *game.ReviewService
	PhotoService    *game.PhotoService
	Attempt         *game.AttemptService
	Progress        *game.LevelProgressService
	Monitor         *game.MonitorService
	Rating          *game.RatingService
	Level           *level.LevelService
	Question        *level.QuestionService
	Answer          *level.AnswerService
	Team            *team.TeamService
	Invitation      *team.InvitationService
	Tournament      *tournament.TournamentService
}

func initServices(db *gorm.DB, repos *repositories, cfg *config.Config, hub *ws.RoomHub, localStorage storage.FileStorage, appCache cache.CacheStore) *services {
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

	photoSvc := game.NewPhotoService(db)

	gameSvc := game.NewGameService(
		db,
		repos.Game,
		repos.GamePassing,
		coAuthorSvc,
		reviewSvc,
		monitorSvc,
		photoSvc,
		hub,
		cfg,
		localStorage,
		appCache,
		repos.User,
		ratingSvc,
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

	// Создаём GameplayHandler один раз для переиспользования
	gameplayHandler := game.NewGameplayHandler(
		gameSvc,
		gamePlaySvc,
		attemptSvc,
		progressSvc,
		monitorSvc,
		hub,
		localStorage,
	)

	return &services{
		Auth:            authSvc,
		User:            userSvc,
		Achiev:          achievSvc,
		OAuth:           oauthSvc,
		PasswordReset:   passResetSvc,
		EmailVerif:      emailVerifSvc,
		Game:            gameSvc,
		GamePlay:        gamePlaySvc,
		GameAdmin:       gameAdminSvc,
		GameplayHandler: gameplayHandler,
		CoAuthor:        coAuthorSvc,
		Review:          reviewSvc,
		PhotoService:    photoSvc,
		Attempt:         attemptSvc,
		Progress:        progressSvc,
		Monitor:         monitorSvc,
		Rating:          ratingSvc,
		Level:           levelSvc,
		Question:        questionSvc,
		Answer:          answerSvc,
		Team:            teamSvc,
		Invitation:      invitationSvc,
		Tournament:      tournamentSvc,
	}
}

// =============================================================================
// РЕГИСТРАЦИЯ МАРШРУТОВ — ИСПОЛЬЗУЮТ ЗАВИСИМОСТИ ИЗ App.Deps
// =============================================================================

func (app *App) registerAdminRoutes(r *gin.RouterGroup) {
	twoFactorSvc := user.NewTwoFactorService()

	// Middleware для проверки 2FA (из domain/user чтобы избежать циклического импорта)
	twoFactorRequired := user.TwoFactorRequired(twoFactorSvc, app.Deps.Repos.User)

	// Применяем 2FA middleware к админ-маршрутам (до регистрации, чтобы сработал на все маршруты)
	adminGroup := r.Group("/admin")
	adminGroup.Use(twoFactorRequired)

	_ = admin.RegisterRoutes(adminGroup, app.DB, app.Config, app.Deps.Services.Auth, app.Deps.Repos.User, app.Deps.Repos.Game, app.Hub)
}

func (app *App) registerUserRoutes(r *gin.RouterGroup) {
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

func (app *App) registerGameRoutes(r *gin.RouterGroup) {
	passingSvc := game.NewGamePassingService(app.DB, app.Deps.Services.Team, app.Deps.Services.CoAuthor, app.Deps.Services.Progress).
		WithHub(app.Hub).
		WithMonitorService(app.Deps.Services.Monitor)

	game.RegisterRoutes(r, &game.GameDeps{
		DB:              app.DB,
		GameService:     app.Deps.Services.Game,
		PassingService:  passingSvc,
		CoAuthorSvc:     app.Deps.Services.CoAuthor,
		AttemptSvc:      app.Deps.Services.Attempt,
		ProgressSvc:     app.Deps.Services.Progress,
		MonitorSvc:      app.Deps.Services.Monitor,
		LocalStorage:    app.LocalStorage,
		Hub:             app.Hub,
		Cfg:             app.Config,
		AuditSvc:        app.Deps.AuditSvc,
		AuthService:     app.Deps.Services.Auth,
		GamePlaySvc:     app.Deps.Services.GamePlay,
		GameAdminSvc:    app.Deps.Services.GameAdmin,
		ReviewService:   app.Deps.Services.Review,
		GameplayHandler: app.Deps.Services.GameplayHandler,
		PhotoService:    app.Deps.Services.PhotoService,
		LevelService:    app.Deps.Services.Level,
	})
}

func (app *App) registerLevelRoutes(r *gin.RouterGroup) {
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

func (app *App) registerTeamRoutes(r *gin.RouterGroup) {
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

func (app *App) registerTournamentRoutes(r *gin.RouterGroup) {
	tournament.RegisterRoutes(r, app.Deps.Services.Tournament, app.Deps.Services.Team, app.Config, app.Deps.Services.Auth)
}

func (app *App) registerCalendarRoutes(r *gin.RouterGroup) {
	calendar.RegisterRoutes(r, app.Deps.Repos.Game)
}

func (app *App) registerMonitorRoutes(r *gin.RouterGroup) {
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
		app.Deps.Services.User,
		app.Deps.Services.Game,
	)
}

func (app *App) registerSocialRoutes(r *gin.RouterGroup) {
	social.RegisterRoutes(r, app.DB, app.Config, app.Deps.Services.Auth)
}

func (app *App) registerExportRoutes(r *gin.RouterGroup) error {
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

func (app *App) registerGameplayRoutes(r *gin.RouterGroup) {
	protected := r.Group("/")
	protected.Use(middleware.AuthRequired(app.Deps.Services.Auth))
	game.RegisterGameplayRoutes(protected, app.Deps.Services.GameplayHandler, app.Deps.Services.CoAuthor)
}
