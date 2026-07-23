// internal/app/app.go
package app

import (
	"fmt"
	"strings"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/notification"
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
	secure := app.Config.TLS.CertFile != "" && app.Config.TLS.KeyFile != ""
	csrfMW := csrf.Middleware(app.Config.Session.Secret, secure, []string{app.Config.Server.BaseURL})
	htmlGroup := r.Group("")
	htmlGroup.Use(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api") {
			c.Next()
			return
		}
		if strings.HasPrefix(c.Request.URL.Path, "/static") {
			c.Next()
			return
		}
		if strings.HasPrefix(c.Request.URL.Path, "/uploads") {
			c.Next()
			return
		}
		c.Header("Cache-Control", "no-store, must-revalidate")
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
	notification.RegisterRoutes(htmlGroup, app.DB, app.Deps.Services.Auth, app.Deps.Hub, app.Deps.Services.SSEMgr)
	return nil
}
