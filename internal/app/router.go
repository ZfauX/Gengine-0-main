// internal/app/router.go
package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"net/http/pprof"
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
	"gengine-0/internal/pkg/i18n"
	"gengine-0/internal/pkg/metrics"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/templatefuncs"

	"gengine-0/internal/config"

	_ "gengine-0/docs"

	corsLib "github.com/gin-contrib/cors"

	csrf "gengine-0/internal/pkg/csrf"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"gorm.io/gorm"
)

func (app *App) setupEngine(r *gin.Engine) error {
	// Два ключа: первый — HMAC-подпись, второй — шифрование (AES). Клиент не сможет прочитать
	// содержимое сессии (pending_user_id, oauth_state, 2fa_verified_*).
	sessionSecret := []byte(app.Config.Session.Secret)
	encryptionKey := sha256.Sum256([]byte(app.Config.Session.Secret + ":enc"))
	store := cookie.NewStore(sessionSecret, encryptionKey[:])
	// Secure-флаг сессии (S-M4): как для JWT-кук — TLS, reverse-proxy
	// (TrustedProxies) или FORCE_SECURE_COOKIE. Иначе session-cookie уходит
	// по HTTP за TLS-терминирующим прокси.
	store.Options(sessions.Options{
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   app.Config.TLS.CertFile != "" || app.Config.Server.TrustedProxies != "" || app.Config.Server.ForceSecureCookie,
	})

	// Загрузчик настроек темы для авторизованных пользователей (используется auth-мидлварями)
	middleware.SetThemeSettingsLoader(func(ctx context.Context, userID uint) any {
		ts, err := user.GetUserThemeSettings(ctx, app.DB, userID)
		if err != nil {
			return user.DefaultThemeSettings()
		}
		return ts
	})
	// Роль из БД вместо JWT-claims (S2): пониженный/удалённый пользователь
	// теряет привилегии немедленно.
	middleware.SetRoleProvider(func(ctx context.Context, userID uint) (string, error) {
		role, err := app.Deps.Repos.User.GetUserRole(ctx, userID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return "", middleware.ErrTokenUserNotFound
			}
			return "", err
		}
		return role, nil
	})

	r.Use(gin.Recovery())
	r.Use(middleware.ErrorHandler())
	r.Use(middleware.LoggerMiddleware())
	// Глобальный per-IP лимит запросов (RATE_LIMIT_GLOBAL). Лимитер
	// инициализируется в cmd/server/main.go (InitGlobalRateLimiter), здесь
	// только регистрируем middleware — без него конфигурация была мёртвой.
	r.Use(middleware.GlobalRateLimit(app.Config.Server.RateLimitWindow, app.Config.Server.RateLimitGlobalRequests))
	// HSTS форсируем, когда сервер гарантированно по HTTPS: собственный TLS
	// либо reverse-proxy (TRUSTED_PROXIES задан) — прокси терминирует TLS.
	forceHSTS := app.Config.TLS.CertFile != "" || app.Config.Server.TrustedProxies != ""
	r.Use(middleware.SecurityHeadersMiddleware(forceHSTS))
	r.Use(sessions.Sessions("gengine_session", store))
	r.Use(i18n.Middleware(i18n.LangRU))
	r.Use(middleware.GzipMiddleware())

	if app.Config.Server.CORSOrigins != "" {
		origins := strings.Split(app.Config.Server.CORSOrigins, ",")
		trimmed := make([]string, 0, len(origins))
		for _, o := range origins {
			o = strings.TrimSpace(o)
			if o == "*" || strings.HasPrefix(o, "http://") || strings.HasPrefix(o, "https://") {
				trimmed = append(trimmed, o)
			} else if o != "" {
				prefixed := "http://" + o
				log.Warn().Str("origin", o).Str("fixed", prefixed).Msg("CORS: origin missing protocol, auto-fixed to " + prefixed)
				trimmed = append(trimmed, prefixed)
			}
		}
		if len(trimmed) > 0 {
			allowAll := false
			for _, o := range trimmed {
				if o == "*" {
					allowAll = true
					break
				}
			}
			r.Use(corsLib.New(corsLib.Config{
				AllowOrigins:     trimmed,
				AllowAllOrigins:  allowAll,
				AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
				AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-CSRF-Token"},
				ExposeHeaders:    []string{"Content-Length", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"},
				AllowCredentials: !allowAll,
				MaxAge:           12 * time.Hour,
			}))
			log.Info().Strs("origins", trimmed).Bool("allow_all", allowAll).Msg("CORS middleware enabled")
		}
	}

	tmpl := template.New("")
	tmpl.Funcs(templatefuncs.FuncMap())
	_, err := tmpl.ParseGlob(filepath.Join(app.BaseDir, "internal", "domain", "*", "templates", "*.html"))
	if err != nil {
		return fmt.Errorf("не удалось загрузить шаблоны: %w", err)
	}
	r.SetHTMLTemplate(tmpl)
	render.SetTemplate(tmpl)
	// Единая версия статики для ?v= в шаблонах (UX5).
	render.SetStaticVersion(config.StaticAssetsVersion)

	if app.Config.Server.GinMode == "debug" {
		render.EnableDevMode(app.BaseDir, templatefuncs.FuncMap())
	}

	r.Use(middleware.MaxBodySize(int64(app.Config.Server.MaxBodySize)))
	r.Use(middleware.ContextTimeout(30 * time.Second))

	// CSRF/Origin-guard для JSON-мутаций /api/* (S-1): проверяет Origin и
	// Sec-Fetch-Site на небезопасных методах — defense in depth поверх
	// SameSite=Strict кук.
	r.Use(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			middleware.APIOriginGuard()(c)
			return
		}
		c.Next()
	})

	// S-1: /swagger и /metrics — только админы с актуальной ролью (AuthRequired
	// перечитывает роль из БД; OptionalAuth доверял claim, и пониженный админ
	// сохранял доступ до истечения токена).
	// S5: добавлен 2FA step-up (как на /admin/*), чтобы украденный JWT не давал
	// доступ к метрикам и полной документации API.
	twoFactorMW := user.TwoFactorRequired(app.Deps.Services.TwoFactor, app.Deps.Repos.User)
	r.GET("/swagger/*any", middleware.AuthRequired(app.Deps.Services.Auth), twoFactorMW, middleware.AdminRequired(), func(c *gin.Context) {
		ginSwagger.WrapHandler(swaggerFiles.Handler)(c)
	})
	r.GET("/metrics", middleware.AuthRequired(app.Deps.Services.Auth), twoFactorMW, middleware.AdminRequired(), func(c *gin.Context) {
		gin.WrapH(promhttp.Handler())(c)
	})

	// Профилирование на реальных данных (pass 29, идея 1): net/http/pprof под
	// той же админ-+2FA защитой, что /metrics. Позволяет снимать CPU/heap/goroutine
	// профили с продакшена: /debug/pprof/ + go tool pprof.
	pprofGroup := r.Group("/debug/pprof", middleware.AuthRequired(app.Deps.Services.Auth), twoFactorMW, middleware.AdminRequired())
	{
		pprofGroup.GET("/", gin.WrapF(pprof.Index))
		pprofGroup.GET("/cmdline", gin.WrapF(pprof.Cmdline))
		pprofGroup.GET("/profile", gin.WrapF(pprof.Profile))
		pprofGroup.GET("/symbol", gin.WrapF(pprof.Symbol))
		pprofGroup.GET("/trace", gin.WrapF(pprof.Trace))
		pprofGroup.GET("/allocs", gin.WrapH(pprof.Handler("allocs")))
		pprofGroup.GET("/block", gin.WrapH(pprof.Handler("block")))
		pprofGroup.GET("/goroutine", gin.WrapH(pprof.Handler("goroutine")))
		pprofGroup.GET("/heap", gin.WrapH(pprof.Handler("heap")))
		pprofGroup.GET("/mutex", gin.WrapH(pprof.Handler("mutex")))
		pprofGroup.GET("/threadcreate", gin.WrapH(pprof.Handler("threadcreate")))
	}

	// RUM (Real User Monitoring, pass 29, идея 2): клиентские Web Vitals.
	// Публичный POST /api/rum с per-IP лимитом — клиент шлёт LCP/INP/CLS/FCP/TTFB.
	// S-1 (pass 31): IPRateLimit вместо APIRateLimit — APIRateLimit пропускает
	// анонимов (userID==0), поэтому прежний лимит 60/min был no-op.
	r.POST("/api/rum", middleware.IPRateLimit(time.Minute, 60), func(c *gin.Context) {
		var payload struct {
			Page   string `json:"page"`
			Vitals struct {
				LCP  float64 `json:"lcp"`
				INP  float64 `json:"inp"`
				CLS  float64 `json:"cls"`
				FCP  float64 `json:"fcp"`
				TTFB float64 `json:"ttfb"`
			} `json:"vitals"`
		}
		if err := c.ShouldBindJSON(&payload); err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		if len(payload.Page) > 128 {
			payload.Page = payload.Page[:128]
		}
		metrics.IncRumPageLoad()
		// Клампим значения (M1, pass 30): клиент может прислать мусор
		// (1e300, NaN-подобные) и отравить гистограммы, по которым стоят алерты.
		// Времена в секундах: LCP/INP/FCP/TTFB — до 60с, CLS — 0..1.
		observeVital := func(name string, v, max float64) {
			if v > 0 && v <= max && !math.IsNaN(v) && !math.IsInf(v, 0) {
				metrics.ObserveRumVital(name, v)
			}
		}
		observeVital("lcp", payload.Vitals.LCP, 60)
		observeVital("inp", payload.Vitals.INP, 60)
		observeVital("cls", payload.Vitals.CLS, 1)
		observeVital("fcp", payload.Vitals.FCP, 60)
		observeVital("ttfb", payload.Vitals.TTFB, 60)
		c.Status(http.StatusNoContent)
	})

	healthChecker := health.NewCheckerWithValkey(app.DB, app.Hub, app.Deps.Cache).
		WithUploadsDir(app.Config.Server.UploadsDir).
		WithSMTPEnabled(app.Config.SMTP.Enabled)
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
					{"loc": app.Config.Server.BaseURL + "/tournaments", "changefreq": "daily", "priority": "0.7"},
					{"loc": app.Config.Server.BaseURL + "/teams", "changefreq": "daily", "priority": "0.5"},
					{"loc": app.Config.Server.BaseURL + "/leaderboard", "changefreq": "daily", "priority": "0.5"},
					{"loc": app.Config.Server.BaseURL + "/auth/login", "changefreq": "monthly", "priority": "0.3"},
					{"loc": app.Config.Server.BaseURL + "/auth/register", "changefreq": "monthly", "priority": "0.3"},
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

	// Chrome DevTools запрашивает этот путь при открытой панели разработчика
	r.GET("/.well-known/appspecific/com.chrome.devtools.json", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{})
	})

	r.NoRoute(func(c *gin.Context) {
		render.RenderErrorPage(c, http.StatusNotFound)
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

	// Cache-Control для /static и /uploads (P4): immutable только для
	// версионированных ?v= URL, иначе короткий max-age / no-cache.
	// Зарегистрирован ДО r.Static, чтобы применялся к статическим файлам (P-C1).
	r.Use(middleware.StaticCacheMiddleware())

	r.Static("/static", filepath.Join(app.BaseDir, app.Config.Server.StaticDir))

	// SEC2: /uploads раздаётся кастомным handler'ом с проверкой прав доступа
	// (avatars — публично; covers/photos — по видимости игры; answers — только
	// участникам команды и менеджерам игры). OptionalAuth прокидывает userID,
	// если пользователь авторизован — аноним получает только публичные файлы.
	uploadsDir := filepath.Join(app.BaseDir, app.Config.Server.UploadsDir)
	uploadsHandler := newUploadsHandler(app.DB, uploadsDir)
	r.GET("/uploads/*filepath", middleware.OptionalAuth(app.Deps.Services.Auth), uploadsHandler.Serve)

	// Service Worker at root scope — controls all pages (offline + push)
	r.GET("/sw.js", func(c *gin.Context) {
		swPath := filepath.Join(app.BaseDir, app.Config.Server.StaticDir, "sw.js")
		c.Header("Service-Worker-Allowed", "/")
		c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
		c.File(swPath)
	})

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
			"Title":         "Главная",
			"CurrentUserID": userID,
			"IsAdmin":       role == "admin",
			"csrf":          csrf.GetToken(c),
		})
	})

	return nil
}

func (app *App) registerAdminRoutes(r *gin.RouterGroup) {
	adminGroup := r.Group("/admin")
	admin.RegisterRoutes(adminGroup, app.Config, app.Deps.Services.Auth, app.Deps.Repos.User, app.Deps.Repos.Game, app.Deps.Services.Game, app.Deps.Repos.Team, app.Deps.Repos.RefreshToken, app.Deps.Services.Backup, app.Deps.AuditSvc, app.Deps.Cache, app.Hub, app.Deps.Services.TwoFactor)
}

func (app *App) registerUserRoutes(r *gin.RouterGroup) {
	user.RegisterRoutes(r, app.Config, app.Deps.Services.Auth, app.Deps.Services.User, app.Deps.Services.PasswordReset, app.Deps.Services.EmailVerif, app.Deps.Services.OAuth, app.Deps.AuditSvc, app.LocalStorage, app.Deps.Services.Email, app.Deps.WebAuthn, app.Deps.Repos.User, app.Deps.Services.PushHandler, app.Deps.Services.TwoFactor, app.Deps.Services.Profile, app.Deps.Repos.Achiev, app.Deps.Services.UserDashboard)
}

func (app *App) registerGameRoutes(r *gin.RouterGroup) {
	game.RegisterRoutes(r, &game.GameDeps{
		GameService: app.Deps.Services.Game, PassingService: app.Deps.Services.GamePassing,
		CoAuthorSvc: app.Deps.Services.CoAuthor, AttemptSvc: app.Deps.Services.Attempt,
		ProgressSvc: app.Deps.Services.Progress, MonitorSvc: app.Deps.Services.Monitor,
		LocalStorage: app.LocalStorage, Hub: app.Hub, Cfg: app.Config,
		AuditSvc: app.Deps.AuditSvc, AuthService: app.Deps.Services.Auth,
		GamePlaySvc: app.Deps.Services.GamePlay, GameAdminSvc: app.Deps.Services.GameAdmin,
		ReviewService: app.Deps.Services.Review, GameplayHandler: app.Deps.Services.GameplayHandler,
		PhotoService: app.Deps.Services.PhotoService, LevelService: app.Deps.Services.Level,
		SimulateSvc: app.Deps.Services.Simulate,
	})
}

func (app *App) registerLevelRoutes(r *gin.RouterGroup) {
	level.RegisterRoutes(r, app.Deps.Services.Level, app.Deps.Services.Question, app.Deps.Services.Answer, app.LocalStorage, app.Hub, app.Config, app.Deps.Services.CoAuthor, app.Deps.Services.Auth)
}

func (app *App) registerTeamRoutes(r *gin.RouterGroup) {
	team.RegisterRoutes(r, app.Deps.Services.Team, app.Deps.Services.Invitation, app.Config, app.LocalStorage, app.Deps.Services.CoAuthor, app.Deps.Services.Auth, app.Hub)
}

func (app *App) registerTournamentRoutes(r *gin.RouterGroup) {
	tournament.RegisterRoutes(r, app.Deps.Services.Tournament, app.Deps.Services.Team, app.Config, app.Deps.Services.Auth)
}

func (app *App) registerCalendarRoutes(r *gin.RouterGroup) {
	calendar.RegisterRoutes(r, app.Deps.Services.CalendarHandler)
}

func (app *App) registerMonitorRoutes(r *gin.RouterGroup) {
	monitor.RegisterRoutes(r, app.Deps.Services.Chat, app.Deps.Services.BlackboxVote, app.Hub, app.Deps.Services.CoAuthor, app.Deps.Services.Monitor, app.Deps.Services.Auth, app.Deps.Services.User, app.Deps.Services.Game)
}

func (app *App) registerSocialRoutes(r *gin.RouterGroup) {
	social.RegisterRoutes(r, app.Deps.Services.Follow, app.Deps.Services.Auth, app.Deps.Services.User)
}

func (app *App) registerExportRoutes(r *gin.RouterGroup) error {
	return export.RegisterRoutes(r, app.Deps.Services.Export, app.LocalStorage, app.Deps.Services.Game, app.Deps.Services.CoAuthor, app.Deps.Services.Auth)
}

func (app *App) registerGameplayRoutes(r *gin.RouterGroup) {
	protected := r.Group("/")
	protected.Use(middleware.AuthRequired(app.Deps.Services.Auth))
	game.RegisterGameplayRoutes(protected, app.Deps.Services.GameplayHandler, app.Deps.Services.CoAuthor, app.Deps.Services.SSEMgr, app.Deps.Repos.Game, app.Deps.Repos.GamePassing)
}
