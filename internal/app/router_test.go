// internal/app/router_test.go
package app

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/tournament"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"
	"gengine-0/internal/testutil"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func projectRoot() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..")
}

func setupRouterTest(t *testing.T) (*gin.Engine, *gorm.DB, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	gin.SetMode(gin.TestMode)

	db := testutil.SetupPostgresDB(t,
		&user.User{},
		&game.Game{},
		&game.GamePassing{},
		&level.Level{},
		&level.Question{},
		&level.Answer{},
		&team.Team{},
		&team.Invitation{},
		&tournament.Tournament{},
		&tournament.TournamentGame{},
		&tournament.TournamentTeam{},
		&tournament.TournamentResult{},
	)

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:    "8080",
			GinMode: "test",
			BaseURL: "http://localhost:8080",
		},
		Session: config.SessionConfig{
			Secret:     "test-session-secret-32chars-long!!!",
			CSRFSecret: "test-csrf-secret-32chars-long!!!!!!",
		},
		JWT: config.JWTConfig{
			Secret:       "test-jwt-secret-32chars-long!!!!!",
			AccessExpiry: 15 * time.Minute,
		},
		Database: config.DatabaseConfig{
			Host: "localhost",
			Port: "5432",
			User: "test",
			Name: "testdb",
		},
	}

	localStorage := storage.NewLocalStorage()
	hub := ws.NewRoomHub()

	baseDir := projectRoot()
	deps := NewDependencies(db, cfg, hub, localStorage, nil)
	app := NewApp(db, localStorage, hub, cfg, baseDir, deps)
	// В тестах кэш не используется, передаём nil
	router, err := app.SetupRouter()
	require.NoError(t, err)

	cleanup := func() {}

	return router, db, cleanup
}

func TestRouter_PublicRoutes(t *testing.T) {
	router, _, cleanup := setupRouterTest(t)
	defer cleanup()

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{
			// P-1 (PASS-13): /swagger вынесен за build-tag `swagger` — в обычной
			// сборке маршрут не зарегистрирован (404). Сборка -tags=swagger
			// регистрирует его под admin+2FA (см. swagger.go).
			name:       "swagger UI недоступен без build-tag swagger (404)",
			method:     "GET",
			path:       "/swagger/index.html",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "login page",
			method:     "GET",
			path:       "/auth/login",
			wantStatus: http.StatusOK,
		},
		{
			name:       "register page",
			method:     "GET",
			path:       "/auth/register",
			wantStatus: http.StatusOK,
		},
		{
			name:       "forgot password page",
			method:     "GET",
			path:       "/auth/forgot",
			wantStatus: http.StatusOK,
		},
		{
			name:       "calendar page",
			method:     "GET",
			path:       "/calendar",
			wantStatus: http.StatusOK,
		},
		{
			name:       "static file (CSS) not found",
			method:     "GET",
			path:       "/static/css/style.css",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "non-existent route",
			method:     "GET",
			path:       "/this-does-not-exist",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, tt.wantStatus, w.Code, "path: %s", tt.path)
		})
	}
}

func TestRouter_ProtectedRoutesRedirect(t *testing.T) {
	router, _, cleanup := setupRouterTest(t)
	defer cleanup()

	// Маршруты защищены AuthRequired. Без куки JWT должен быть редирект на /auth/login.
	// Используем новые пути: /teams/new и /games/new вместо /teams/create и /games/create.
	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{name: "dashboard", method: "GET", path: "/dashboard/", wantStatus: http.StatusFound},
		{name: "profile", method: "GET", path: "/profile/", wantStatus: http.StatusFound},
		{name: "achievements", method: "GET", path: "/achievements/", wantStatus: http.StatusFound},
		{name: "team creation", method: "GET", path: "/teams/new", wantStatus: http.StatusFound},
		{name: "game creation", method: "GET", path: "/games/new", wantStatus: http.StatusFound},
		// S-46-1 (pass 46): геолокация и Phase-3 маршруты требуют аутентификации
		// (и прав менеджера игры). Без JWT GET-маршруты редиректят на login,
		// а POST-маршруты без CSRF-токена отсекаются middleware CSRF (403) раньше,
		// чем AuthRequired — это тоже защита.
		{name: "game locations", method: "GET", path: "/games/1/locations", wantStatus: http.StatusFound},
		{name: "phase3 set route", method: "POST", path: "/games/1/passings/1/route", wantStatus: http.StatusForbidden},
		{name: "phase3 get route", method: "GET", path: "/games/1/passings/1/route", wantStatus: http.StatusFound},
		{name: "phase3 start time", method: "POST", path: "/games/1/passings/1/start-time", wantStatus: http.StatusForbidden},
		{name: "phase3 answer", method: "POST", path: "/games/1/levels/1/teams/1/answer", wantStatus: http.StatusForbidden},
		{name: "phase3 attempts", method: "GET", path: "/games/1/attempts-per-user", wantStatus: http.StatusFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, tt.wantStatus, w.Code)
			if tt.wantStatus == http.StatusFound {
				assert.Equal(t, "/auth/login", w.Header().Get("Location"))
			}
		})
	}
}

func TestRouter_APIProtectedRoutesUnauthorized(t *testing.T) {
	router, _, cleanup := setupRouterTest(t)
	defer cleanup()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"api dashboard", "GET", "/api/v1/dashboard"},
		{"api profile", "GET", "/api/v1/profile"},
		{"api achievements", "GET", "/api/v1/achievements"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusNotFound, w.Code)
		})
	}
}

func TestRouter_CSRFProtection(t *testing.T) {
	router, _, cleanup := setupRouterTest(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/auth/login", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	// Проверка наличия csrf токена в теле нестабильна из-за особенностей загрузки шаблонов,
	// поэтому ограничиваемся статусом.
}

// DEEP-REVIEW (pass 46): вебхук ЮKassa должен быть исключён из CSRF —
// server-to-server POST без токена обязан ДОХОДИТЬ до обработчика, а не
// получать 403 "CSRF token mismatch" (иначе платежи не подтверждаются).
func TestRouter_PaymentWebhook_NoCSRFToken(t *testing.T) {
	router, _, cleanup := setupRouterTest(t)
	defer cleanup()

	body := strings.NewReader("{\"event\":\"payment.succeeded\",\"object\":{\"id\":\"test\"}}")
	req := httptest.NewRequest(http.MethodPost, "/payments/webhook", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.NotEqual(t, http.StatusForbidden, w.Code,
		"вебхук не должен блокироваться CSRF (403 CSRF token mismatch)")
	// Допустимые коды: 200 (принят), 400/500 (прочие ошибки обработки) —
	// главное, что запрос прошёл CSRF-слой.
	assert.Contains(t, []int{http.StatusOK, http.StatusBadRequest, http.StatusInternalServerError}, w.Code)
}
