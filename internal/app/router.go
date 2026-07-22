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
	"gengine-0/internal/pkg/health"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/templatefuncs"

	_ "gengine-0/docs"

	csrf "gengine-0/internal/pkg/csrf"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

func (app *App) setupEngine(r *gin.Engine) error {
	store := cookie.NewStore([]byte(app.Config.Session.Secret))
	store.Options(sessions.Options{
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	r.Use(gin.Recovery())
	r.Use(middleware.ErrorHandler())
	r.Use(middleware.LoggerMiddleware())
	r.Use(sessions.Sessions("gengine_session", store))

	tmpl := template.New("")
	tmpl.Funcs(templatefuncs.FuncMap())
	_, err := tmpl.ParseGlob(filepath.Join(app.BaseDir, "internal", "domain", "*", "templates", "*.html"))
	if err != nil {
		return fmt.Errorf("не удалось загрузить шаблоны: %w", err)
	}
	r.SetHTMLTemplate(tmpl)
	render.SetTemplate(tmpl)

	if app.Config.Server.GinMode == "debug" {
		render.EnableDevMode(app.BaseDir, templatefuncs.FuncMap(), func(t *template.Template) {
			r.SetHTMLTemplate(t)
		})
	}

	r.Use(middleware.ContextTimeout(30 * time.Second))

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

	healthChecker := health.NewCheckerWithValkey(app.DB, app.Hub, app.Deps.Cache).WithUploadsDir("uploads")
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

	r.GET("/offline", func(c *gin.Context) {
		render.Page(c, http.StatusOK, "offline.html", gin.H{"Title": "Нет соединения"})
	})

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
		if err := r.SetTrustedProxies(nil); err != nil {
			log.Error().Err(err).Msg("router: SetTrustedProxies error")
		}
	}

	r.Static("/static", filepath.Join(app.BaseDir, app.Config.Server.StaticDir))
	r.Static("/uploads", filepath.Join(app.BaseDir, app.Config.Server.UploadsDir))

	r.GET("/", middleware.OptionalAuth(app.Deps.Services.Auth), func(c *gin.Context) {
		var userID uint
		var role string
		if id, ok := c.Get("userID"); ok {
			if v, ok := id.(uint); ok {
				userID = v
			}
		}
		if r, ok := c.Get("role"); ok {
			if v, ok := r.(string); ok {
				role = v
			}
		}
		if userID == 0 {
			c.Header("Cache-Control", "public, max-age=60, s-maxage=120")
		} else {
			c.Header("Cache-Control", "no-cache, private")
		}
		render.Page(c, http.StatusOK, "home.html", gin.H{
			"CurrentUserID": userID,
			"IsAdmin":       role == "admin",
			"csrf":          csrf.GetToken(c),
		})
	})

	return nil
}

func (app *App) registerAdminRoutes(r *gin.RouterGroup) {
	twoFactorSvc := user.NewTwoFactorService()
	twoFactorRequired := user.TwoFactorRequired(twoFactorSvc, app.Deps.Repos.User)
	adminGroup := r.Group("/admin")
	adminGroup.Use(twoFactorRequired)
	_ = admin.RegisterRoutes(adminGroup, app.DB, app.Config, app.Deps.Services.Auth, app.Deps.Repos.User, app.Deps.Repos.Game, app.Hub)
}

func (app *App) registerUserRoutes(r *gin.RouterGroup) {
	user.RegisterRoutes(r, app.Config, app.Deps.Services.Auth, app.Deps.Services.User, app.Deps.Services.PasswordReset, app.Deps.Services.EmailVerif, app.Deps.Services.OAuth, app.Deps.AuditSvc, app.DB, app.LocalStorage, app.Deps.Services.Email)
}

func (app *App) registerGameRoutes(r *gin.RouterGroup) {
	passingSvc := game.NewGamePassingService(app.DB, app.Deps.Services.Team, app.Deps.Services.CoAuthor, app.Deps.Services.Progress).
		WithHub(app.Hub).
		WithMonitorService(app.Deps.Services.Monitor)
	game.RegisterRoutes(r, &game.GameDeps{
		DB: app.DB, GameService: app.Deps.Services.Game, PassingService: passingSvc,
		CoAuthorSvc: app.Deps.Services.CoAuthor, AttemptSvc: app.Deps.Services.Attempt,
		ProgressSvc: app.Deps.Services.Progress, MonitorSvc: app.Deps.Services.Monitor,
		LocalStorage: app.LocalStorage, Hub: app.Hub, Cfg: app.Config,
		AuditSvc: app.Deps.AuditSvc, AuthService: app.Deps.Services.Auth,
		GamePlaySvc: app.Deps.Services.GamePlay, GameAdminSvc: app.Deps.Services.GameAdmin,
		ReviewService: app.Deps.Services.Review, GameplayHandler: app.Deps.Services.GameplayHandler,
		PhotoService: app.Deps.Services.PhotoService, LevelService: app.Deps.Services.Level,
	})
}

func (app *App) registerLevelRoutes(r *gin.RouterGroup) {
	level.RegisterRoutes(r, app.Deps.Services.Level, app.Deps.Services.Question, app.Deps.Services.Answer, app.LocalStorage, app.Hub, app.Config, app.Deps.Services.CoAuthor, app.Deps.Services.Auth)
}

func (app *App) registerTeamRoutes(r *gin.RouterGroup) {
	team.RegisterRoutes(r, app.Deps.Services.Team, app.Deps.Services.Invitation, app.Config, app.LocalStorage, app.Deps.Services.CoAuthor, app.Deps.Services.Auth)
}

func (app *App) registerTournamentRoutes(r *gin.RouterGroup) {
	tournament.RegisterRoutes(r, app.Deps.Services.Tournament, app.Deps.Services.Team, app.Config, app.Deps.Services.Auth)
}

func (app *App) registerCalendarRoutes(r *gin.RouterGroup) {
	calendar.RegisterRoutes(r, app.Deps.Repos.Game)
}

func (app *App) registerMonitorRoutes(r *gin.RouterGroup) {
	monitor.RegisterRoutes(r, app.DB, app.Hub, app.Config, app.Deps.Services.CoAuthor, app.Deps.Services.Monitor, app.Deps.Services.Attempt, app.Deps.Services.Progress, app.Deps.Services.Auth, app.Deps.Repos.Game, app.Deps.Services.User, app.Deps.Services.Game)
}

func (app *App) registerSocialRoutes(r *gin.RouterGroup) {
	social.RegisterRoutes(r, app.DB, app.Config, app.Deps.Services.Auth)
}

func (app *App) registerExportRoutes(r *gin.RouterGroup) error {
	return export.RegisterRoutes(r, app.DB, app.LocalStorage, app.Config, app.Deps.Services.Game, app.Deps.Services.CoAuthor, app.Deps.Services.Auth)
}

func (app *App) registerGameplayRoutes(r *gin.RouterGroup) {
	protected := r.Group("/")
	protected.Use(middleware.AuthRequired(app.Deps.Services.Auth))
	game.RegisterGameplayRoutes(protected, app.Deps.Services.GameplayHandler, app.Deps.Services.CoAuthor, app.Deps.Services.SSEMgr)
}
