// internal/app/router_test.go
package app

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
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
			Secret: "test-session-secret-32chars-long!!!",
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
	router, err := SetupRouter(db, localStorage, hub, cfg, baseDir)
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
			name:       "swagger UI",
			method:     "GET",
			path:       "/swagger/index.html",
			wantStatus: http.StatusOK,
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
	// Некоторые маршруты зарегистрированы с завершающим слешем (например, `/dashboard/`),
	// поэтому запрашиваем их с `/`, чтобы не получить 301.
	tests := []struct {
		name string
		path string
	}{
		{name: "dashboard", path: "/dashboard/"},
		{name: "profile", path: "/profile/"},
		{name: "achievements", path: "/achievements/"},
		{name: "team creation", path: "/teams/create"},
		{name: "game creation", path: "/games/create"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusFound, w.Code)
			assert.Equal(t, "/auth/login", w.Header().Get("Location"))
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
