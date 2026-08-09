// internal/app/app.go
package app

import (
	"fmt"
	"strings"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/notification"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/audit"
	"gengine-0/internal/pkg/cache"
	csrf "gengine-0/internal/pkg/csrf"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Dependencies struct {
	Repos    *repositories
	Services *services
	AuditSvc *audit.Service
	WebAuthn *user.WebAuthnHandler
	Cache    cache.CacheStore
	Hub      *ws.RoomHub
}

func NewDependencies(db *gorm.DB, cfg *config.Config, hub *ws.RoomHub, localStorage storage.FileStorage, appCache cache.CacheStore) *Dependencies {
	repos := initRepositories(db)
	services, svcErr := initServices(db, repos, cfg, hub, localStorage, appCache)
	if svcErr != nil {
		panic(svcErr)
	}
	auditSvc := audit.NewService(db)
	webauthnHandler, err := user.NewWebAuthnHandler(cfg, services.Auth, repos.User, repos.WebAuthn, auditSvc)
	if err != nil {
		panic(err)
	}

	return &Dependencies{
		Repos:    repos,
		Services: services,
		AuditSvc: auditSvc,
		WebAuthn: webauthnHandler,
		Cache:    appCache,
		Hub:      hub,
	}
}

type App struct {
	Config       *config.Config
	DB           *gorm.DB
	LocalStorage storage.FileStorage
	Hub          *ws.RoomHub
	BaseDir      string
	Deps         *Dependencies
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

	// HTML-маршруты — с CSRF-защитой
	// API-маршруты (/api/*) CSRF не требуют — используют JWT-аутентификацию
	// S-4 (pass 35): Secure-флаг CSRF-куки выровнен с session-store — учитывает
	// TLS, reverse-proxy (TrustedProxies) и FORCE_SECURE_COOKIE. Раньше CSRF-кука
	// была Secure только при собственном TLS и уходила по HTTP за TLS-терминатором.
	secure := app.Config.TLS.CertFile != "" || app.Config.Server.TrustedProxies != "" || app.Config.Server.ForceSecureCookie
	csrfMW := csrf.Middleware(app.Config.Session.CSRFSecret, secure, []string{app.Config.Server.BaseURL})
	htmlGroup := r.Group("")
	htmlGroup.Use(func(c *gin.Context) {
		// /auth/webauthn/* в целом НЕ исключаем из CSRF: register/begin и
		// register/finish аутентифицированы и требуют токен. Без CSRF-защиты
		// только публичные login/begin и login/finish (WebAuthn challenge сам
		// привязан к сессии, CSRF здесь невозможен).
		skip := []string{"/api", "/static", "/uploads", "/ws", "/auth/webauthn/login/begin", "/auth/webauthn/login/finish"}
		for _, prefix := range skip {
			if strings.HasPrefix(c.Request.URL.Path, prefix) {
				c.Next()
				return
			}
		}
		csrfMW(c)
	})

	if err := app.registerAllRoutes(r, htmlGroup); err != nil {
		return nil, err
	}

	return r, nil
}

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
	notification.RegisterRoutes(htmlGroup, app.Config, app.Deps.Services.Notification, app.Deps.Services.Auth, app.Deps.Hub)
	return nil
}
