// internal/app/router.go
package app

import (
	"fmt"
	"html/template"
	"path/filepath"

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
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"github.com/utrack/gin-csrf"
	"gorm.io/gorm"
)

// SetupRouter настраивает все middleware, роуты и возвращает готовый *gin.Engine.
// baseDir — корень проекта (для поиска шаблонов и статики).
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

	// Инициализация сервисов
	userAuthSvc := user.NewAuthService(db, cfg)
	coAuthorSvc := game.NewCoAuthorService(db)
	reviewSvc := game.NewReviewService(db)
	attemptSvc := game.NewAttemptService(db)
	progressSvc := game.NewLevelProgressService(db)
	monitorSvc := game.NewMonitorService(db)
	gameSvc := game.NewGameService(db, coAuthorSvc, reviewSvc, monitorSvc, hub, attemptSvc, progressSvc, cfg)

	// Регистрация маршрутов
	auditSvc := admin.RegisterRoutes(r, db, cfg)
	user.RegisterRoutes(r, db, cfg, auditSvc)
	game.RegisterRoutes(r, db, localStorage, hub, cfg, coAuthorSvc, attemptSvc, progressSvc, monitorSvc, auditSvc)
	level.RegisterRoutes(r, db, localStorage, hub, cfg, coAuthorSvc, gameSvc)
	team.RegisterRoutes(r, db, cfg, localStorage, coAuthorSvc)

	gameplayHandler := game.NewGameplayHandler(gameSvc, attemptSvc, progressSvc, monitorSvc, hub, localStorage, db)
	protected := r.Group("/")
	protected.Use(middleware.AuthRequired(userAuthSvc))
	game.RegisterGameplayRoutes(protected, gameplayHandler, coAuthorSvc)

	monitor.RegisterRoutes(r, db, hub, cfg, coAuthorSvc, monitorSvc, attemptSvc, progressSvc)
	social.RegisterRoutes(r, db, cfg)
	calendar.RegisterRoutes(r, db)
	export.RegisterRoutes(r, db, localStorage, cfg, gameSvc, coAuthorSvc)
	tournament.RegisterRoutes(r, db, cfg)

	return r
}
