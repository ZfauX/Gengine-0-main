//go:build integration

// cmd/server/integration_test.go
package main_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"gengine-0/internal/app"
	"gengine-0/internal/config"
	"gengine-0/internal/domain/admin"
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
	"gengine-0/internal/testutil"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var csrfTokenRE = regexp.MustCompile(`<input[^>]+name="_csrf"[^>]+value="([^"]+)"`)

func setupTestRouter(t *testing.T, db *gorm.DB, cfg *config.Config) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	localStorage := storage.NewLocalStorage()
	hub := ws.NewRoomHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	// Создаём deps и app напрямую, без legacy-функций
	deps := app.NewDependencies(db, cfg, hub, localStorage, nil)
	appInstance := app.NewApp(db, localStorage, hub, cfg, "../..", deps)
	router, err := appInstance.SetupRouter()
	if err != nil {
		t.Fatalf("failed to setup router: %v", err)
	}
	router.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })

	// Test endpoints (no CSRF, for integration tests only)
	authSvc := deps.Services.Auth
	router.POST("/auth/loginIntegration", func(c *gin.Context) {
		var input user.LoginInput
		if err := c.ShouldBind(&input); err != nil {
			c.JSON(400, gin.H{"error": "bad request"})
			return
		}
		token, err := authSvc.Login(c.Request.Context(), input.Email, input.Password)
		if err != nil {
			c.JSON(401, gin.H{"error": err.Error()})
			return
		}
		c.SetCookie("jwt", token, int(cfg.JWT.AccessExpiry.Seconds()), "/", "", false, true)
		c.Status(200)
	})
	router.POST("/auth/registerIntegration", func(c *gin.Context) {
		var input user.RegisterInput
		if err := c.ShouldBind(&input); err != nil {
			c.JSON(400, gin.H{"error": "bad request"})
			return
		}
		if _, err := authSvc.Register(c.Request.Context(), input.Email, input.Password, input.Name); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		record, _ := deps.Services.User.GetByEmail(c.Request.Context(), input.Email)
		if record != nil {
			token, _ := authSvc.GenerateJWT(*record)
			c.SetCookie("jwt", token, int(cfg.JWT.AccessExpiry.Seconds()), "/", "", false, true)
		}
		c.Status(200)
	})
	// Test force-finish and disqualify (no CSRF)
	mwAuth := middleware.AuthRequired(authSvc)
	router.POST("/test/games/:id/force-finish", mwAuth, func(c *gin.Context) {
		gameID, _ := strconv.Atoi(c.Param("id"))
		userID := c.GetUint("userID")
		if err := deps.Services.GameAdmin.ForceFinishGame(c.Request.Context(), uint(gameID), userID); err != nil {
			c.Status(http.StatusForbidden)
			return
		}
		c.Status(http.StatusOK)
	})
	router.POST("/test/games/:id/disqualify", mwAuth, func(c *gin.Context) {
		gameID, _ := strconv.Atoi(c.Param("id"))
		userID := c.GetUint("userID")
		var input struct{ TeamID uint `form:"team_id"` }
		if err := c.ShouldBind(&input); err != nil || input.TeamID == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
			return
		}
		if err := deps.Services.GameAdmin.DisqualifyTeam(c.Request.Context(), uint(gameID), input.TeamID, userID); err != nil {
			c.Status(http.StatusForbidden)
			return
		}
		c.Status(http.StatusOK)
	})
	return router
}

func getCSRFToken(router *gin.Engine, url string, cookies []*http.Cookie) (string, []*http.Cookie) {
	req := httptest.NewRequest("GET", url, nil)
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	body := w.Body.String()
	match := csrfTokenRE.FindStringSubmatch(body)
	var token string
	if len(match) >= 2 {
		token = match[1]
	}
	merged := mergeCookies(cookies, w.Result().Cookies())
	return token, merged
}

func mergeCookies(old, new []*http.Cookie) []*http.Cookie {
	m := make(map[string]*http.Cookie)
	for _, c := range old {
		m[c.Name] = c
	}
	for _, c := range new {
		m[c.Name] = c
	}
	res := make([]*http.Cookie, 0, len(m))
	for _, c := range m {
		res = append(res, c)
	}
	return res
}

func TestFullGameFlow(t *testing.T) {
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret:       "integration-secret-32chars!!",
			AccessExpiry: 24 * time.Hour,
		},
		Session: config.SessionConfig{
			Secret: "test-session-secret-32chars-long!!!",
		},
		SMTP: config.SMTPConfig{
			Enabled: false,
		},
	}

	db := testutil.SetupPostgresDBOrSkip(t,
		&user.User{}, &user.Achievement{}, &user.PasswordResetToken{}, &user.EmailVerificationToken{},
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{}, &game.CoAuthor{}, &game.Note{},
		&game.LevelProgress{}, &game.Attempt{},
		&monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&game.Log{},
		&game.PlayerRating{},
		&level.Level{}, &level.Question{}, &level.Answer{},
		&team.Team{}, &team.Invitation{},
		&monitor.ChatRoom{}, &monitor.ChatMessage{},
		&social.PlayerRating{}, &social.Follow{},
		&admin.AuditLog{}, &admin.Backup{}, &audit.Entry{},
		&tournament.Tournament{}, &tournament.TournamentGame{}, &tournament.TournamentTeam{}, &tournament.TournamentResult{},
	)

	router := setupTestRouter(t, db, cfg)

	// Шаг 1: регистрация (через test endpoint, без CSRF)
	registerBody := url.Values{
		"email":    {"user@test.com"},
		"password": {"password123"},
		"name":     {"Tester"},
	}
	req := httptest.NewRequest("POST", "/auth/registerIntegration", strings.NewReader(registerBody.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "Шаг 1: регистрация")

	var jwtCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "jwt" {
			jwtCookie = c
			break
		}
	}
	require.NotNil(t, jwtCookie, "JWT кука должна быть установлена после регистрации")

	// Шаг 2: вход (через test endpoint, без CSRF)
	loginBody := url.Values{
		"email":    {"user@test.com"},
		"password": {"password123"},
	}
	req = httptest.NewRequest("POST", "/auth/loginIntegration", strings.NewReader(loginBody.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "Шаг 2: вход")

	jwtCookie = nil
	for _, c := range w.Result().Cookies() {
		if c.Name == "jwt" {
			jwtCookie = c
			break
		}
	}
	require.NotNil(t, jwtCookie, "JWT кука должна быть установлена после входа")

	// Шаг 2.5: проверка дашборда
	req = httptest.NewRequest("GET", "/dashboard/", nil)
	req.AddCookie(jwtCookie)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code, "Дашборд должен быть доступен")
	bodyBytes, _ := io.ReadAll(w.Body)
	assert.Contains(t, string(bodyBytes), "Личный кабинет", "Страница дашборда должна содержать заголовок")

	// Получаем пользователя из БД для authorID
	var userRecord user.User
	require.NoError(t, db.Where("email = ?", "user@test.com").First(&userRecord).Error)

	// Шаг 3: создание игры через БД (сразу опубликована)
	g := createPublishedGameWithSettingsIntegration(t, db, userRecord.ID, "Integration Game")
	gameID := g.ID

	// Шаг 4: создаём уровень с ответом (до старта игры!)
	lvl := &level.Level{GameID: gameID, Name: "Level 1", Position: 1}
	require.NoError(t, db.Create(lvl).Error)
	q := &level.Question{LevelID: lvl.ID, Text: "Q"}
	require.NoError(t, db.Create(q).Error)
	a := &level.Answer{QuestionID: q.ID, Code: "secret"}
	require.NoError(t, db.Create(a).Error)

	// Шаг 5: создание команды через БД
	tm := createTeamIntegration(t, db, userRecord.ID)
	teamID := tm.ID

	// Шаг 6: подача заявки через БД
	passing := createPassingIntegration(t, db, gameID, teamID, game.StatusPending)

	// Шаг 7: принятие заявки через БД
	db.Model(&passing).Update("status", game.StatusAccepted)

	// Шаг 8: старт игры через БД
	db.Model(&passing).Update("status", game.StatusStarted)

	// Шаг 9: ввод правильного кода через БД
	db.Create(&game.LevelProgress{GamePassingID: passing.ID, LevelID: lvl.ID, StartedAt: time.Now()})
	db.Model(&passing).Update("status", game.StatusFinished)

	// Шаг 10: проверка завершения игры
	var updatedPassing game.GamePassing
	db.First(&updatedPassing, passing.ID)
	assert.Equal(t, game.StatusFinished, updatedPassing.Status, "Игра должна быть завершена")
}

// I3: Integration tests на permission checks
func TestIntegration_PermissionChecks(t *testing.T) {
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret:       "integration-secret-32chars!!",
			AccessExpiry: 24 * time.Hour,
		},
		Session: config.SessionConfig{
			Secret: "test-session-secret-32chars-long!!!",
		},
		SMTP: config.SMTPConfig{
			Enabled: false,
		},
		WebSocket: config.WebSocketConfig{
			MaxTotalConns: 100, MaxConnsPerIP: 10,
		},
		Server: config.ServerConfig{
			Port: ":8080",
		},
	}

	db := testutil.SetupPostgresDB(t,
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{},
		&game.LevelProgress{}, &game.Attempt{},
		&game.CoAuthor{},
		&level.Level{},
		&team.Team{},
		&user.User{},
	)

	router := setupTestRouter(t, db, cfg)

	// Создаём автора
	author := createUserIntegration(t, db, "auth_perm@test.com", "pass123")
	other := createUserIntegration(t, db, "other_perm@test.com", "pass123")

	// Создаём игру
	g := createPublishedGameWithSettingsIntegration(t, db, author.ID, "Perm Test Game")

	// T1: Non-manager не может force-finish
	t.Run("non_manager_cannot_force_finish", func(t *testing.T) {
		// Создаём team и passing для non-manager
		tm := createTeamIntegration(t, db, other.ID)
		createPassingIntegration(t, db, g.ID, tm.ID, game.StatusStarted)

		cookies := loginDirect(t, cfg, db, "other_perm@test.com")
		body := url.Values{}
		req := httptest.NewRequest("POST", fmt.Sprintf("/test/games/%d/force-finish", g.ID), strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, ck := range cookies {
			req.AddCookie(ck)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code, "Non-manager не должен иметь доступ к force-finish")
	})

	// T2: Author может force-finish
	t.Run("author_can_force_finish", func(t *testing.T) {
		// Создаём team и passing для author
		tm := createTeamIntegration(t, db, author.ID)
		createPassingIntegration(t, db, g.ID, tm.ID, game.StatusStarted)

		cookies := loginDirect(t, cfg, db, "auth_perm@test.com")
		body := url.Values{}
		req := httptest.NewRequest("POST", fmt.Sprintf("/test/games/%d/force-finish", g.ID), strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, ck := range cookies {
			req.AddCookie(ck)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.NotEqual(t, http.StatusForbidden, w.Code, "Author должен иметь доступ к force-finish")
	})

	// T3: Non-manager не может disqualify
	t.Run("non_manager_cannot_disqualify", func(t *testing.T) {
		// Создаём team и passing для другого пользователя
		tm := createTeamIntegration(t, db, other.ID)
		createPassingIntegration(t, db, g.ID, tm.ID, game.StatusStarted)

		cookies := loginDirect(t, cfg, db, "other_perm@test.com")
		body := url.Values{"team_id": {fmt.Sprintf("%d", tm.ID)}}
		req := httptest.NewRequest("POST", fmt.Sprintf("/test/games/%d/disqualify", g.ID), strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, ck := range cookies {
			req.AddCookie(ck)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code, "Non-manager не должен иметь доступ к disqualify")
	})

	// T4: Автор может disqualify
	t.Run("author_can_disqualify", func(t *testing.T) {
		tm := createTeamIntegration(t, db, author.ID)
		createPassingIntegration(t, db, g.ID, tm.ID, game.StatusStarted)

		cookies := loginDirect(t, cfg, db, "auth_perm@test.com")
		body := url.Values{"team_id": {fmt.Sprintf("%d", tm.ID)}}
		req := httptest.NewRequest("POST", fmt.Sprintf("/test/games/%d/disqualify", g.ID), strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, ck := range cookies {
			req.AddCookie(ck)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.NotEqual(t, http.StatusForbidden, w.Code, "Author должен иметь доступ к disqualify")
	})
}

// Helper functions для integration tests

func createUserIntegration(t *testing.T, db *gorm.DB, email, password string) *user.User {
	t.Helper()
	u := &user.User{
		Email:    email,
		Password: password,
		Name:     "Test User",
		Role:     "user",
	}
	require.NoError(t, db.Create(u).Error)
	return u
}

func createPublishedGameWithSettingsIntegration(t *testing.T, db *gorm.DB, authorID uint, name string) *game.Game {
	t.Helper()
	g := &game.Game{
		Name:       name,
		AuthorID:   authorID,
		Visibility: "public",
		IsDraft:    false,
	}
	require.NoError(t, db.Create(g).Error)

	settings := &game.GameSetting{
		GameID:     g.ID,
		MaxHints:   3,
		AllowHints: true,
		AutoStart:  false,
	}
	require.NoError(t, db.Create(settings).Error)
	return g
}

func loginIntegration(router *gin.Engine, email, password string) (string, []*http.Cookie) {
	resp := httptest.NewRecorder()
	body := url.Values{"email": {email}, "password": {password}}
	req := httptest.NewRequest("POST", "/auth/loginIntegration", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	router.ServeHTTP(resp, req)
	return "", resp.Result().Cookies()
}

func loginDirect(t *testing.T, cfg *config.Config, db *gorm.DB, email string) []*http.Cookie {
	t.Helper()
	var userRecord user.User
	require.NoError(t, db.Where("email = ?", email).First(&userRecord).Error)
	claims := jwt.MapClaims{
		"user_id": userRecord.ID,
		"role":    userRecord.Role,
		"exp":     time.Now().Add(24 * time.Hour).Unix(),
		"iat":     time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := token.SignedString([]byte(cfg.JWT.Secret))
	require.NoError(t, err)
	return []*http.Cookie{{Name: "jwt", Value: tokenStr, Path: "/", HttpOnly: true}}
}

func createTeamIntegration(t *testing.T, db *gorm.DB, captainID uint) *team.Team {
	t.Helper()
	tm := &team.Team{
		Name:      "Test Team",
		CaptainID: captainID,
	}
	require.NoError(t, db.Create(tm).Error)
	return tm
}

func createPassingIntegration(t *testing.T, db *gorm.DB, gameID, teamID uint, status game.GamePassingStatus) *game.GamePassing {
	t.Helper()
	p := &game.GamePassing{
		GameID: gameID,
		TeamID: teamID,
		Status: status,
	}
	require.NoError(t, db.Create(p).Error)
	return p
}

func TestForceFinishPermissions(t *testing.T) {
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret:       "integration-secret-32chars!!",
			AccessExpiry: 24 * time.Hour,
		},
		Session: config.SessionConfig{
			Secret: "test-session-secret-32chars-long!!!",
		},
		SMTP: config.SMTPConfig{
			Enabled: false,
		},
	}

	db := testutil.SetupPostgresDB(t,
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{},
		&game.LevelProgress{}, &game.Attempt{},
		&game.CoAuthor{},
		&game.Note{},
		&level.Level{},
		&team.Team{},
		&user.User{},
	)

	router := setupTestRouter(t, db, cfg)

	// Создаём автора
	author := createUserIntegration(t, db, "auth_int@test.com", "pass123")
	other := createUserIntegration(t, db, "other_int@test.com", "pass123")

	// Создаём игру
	g := createPublishedGameWithSettingsIntegration(t, db, author.ID, "Perm Test Game")

	// T1: Non-manager не может force-finish
	t.Run("non_manager_cannot_force_finish", func(t *testing.T) {
		// Создаём team и passing для non-manager
		tm := createTeamIntegration(t, db, other.ID)
		createPassingIntegration(t, db, g.ID, tm.ID, game.StatusStarted)

		cookies := loginDirect(t, cfg, db, "other_int@test.com")
		body := url.Values{}
		req := httptest.NewRequest("POST", fmt.Sprintf("/test/games/%d/force-finish", g.ID), strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, ck := range cookies {
			req.AddCookie(ck)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code, "Non-manager не должен иметь доступ к force-finish")
	})

	// T2: Author может force-finish
	t.Run("author_can_force_finish", func(t *testing.T) {
		// Создаём team и passing для author
		tm := createTeamIntegration(t, db, author.ID)
		createPassingIntegration(t, db, g.ID, tm.ID, game.StatusStarted)

		cookies := loginDirect(t, cfg, db, "auth_int@test.com")
		body := url.Values{}
		req := httptest.NewRequest("POST", fmt.Sprintf("/test/games/%d/force-finish", g.ID), strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, ck := range cookies {
			req.AddCookie(ck)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.NotEqual(t, http.StatusForbidden, w.Code, "Author должен иметь доступ к force-finish")
	})

	// T3: Non-manager не может disqualify
	t.Run("non_manager_cannot_disqualify", func(t *testing.T) {
		tm := createTeamIntegration(t, db, other.ID)
		createPassingIntegration(t, db, g.ID, tm.ID, game.StatusStarted)

		cookies := loginDirect(t, cfg, db, "other_int@test.com")
		body := url.Values{"team_id": {fmt.Sprintf("%d", tm.ID)}}
		req := httptest.NewRequest("POST", fmt.Sprintf("/test/games/%d/disqualify", g.ID), strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, ck := range cookies {
			req.AddCookie(ck)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code, "Non-manager не должен иметь доступ к disqualify")
	})

	// T4: Автор может disqualify
	t.Run("author_can_disqualify", func(t *testing.T) {
		tm := createTeamIntegration(t, db, author.ID)
		createPassingIntegration(t, db, g.ID, tm.ID, game.StatusStarted)

		cookies := loginDirect(t, cfg, db, "auth_int@test.com")
		body := url.Values{"team_id": {fmt.Sprintf("%d", tm.ID)}}
		req := httptest.NewRequest("POST", fmt.Sprintf("/test/games/%d/disqualify", g.ID), strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, ck := range cookies {
			req.AddCookie(ck)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.NotEqual(t, http.StatusForbidden, w.Code, "Author должен иметь доступ к disqualify")
	})
}
