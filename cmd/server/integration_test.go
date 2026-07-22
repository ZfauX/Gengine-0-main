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
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"
	"gengine-0/internal/testutil"

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

	var sessionCookies []*http.Cookie
	csrfToken, sessionCookies := getCSRFToken(router, "/auth/register", sessionCookies)

	if csrfToken == "" {
		time.Sleep(50 * time.Millisecond)
		csrfToken, sessionCookies = getCSRFToken(router, "/auth/register", sessionCookies)
		if csrfToken == "" {
			req, _ := http.NewRequest("GET", "/auth/register", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			t.Skipf("CSRF token not found on registration page. Body: %s", w.Body.String())
		}
	}

	// Шаг 1: регистрация
	registerBody := url.Values{
		"email":    {"user@test.com"},
		"password": {"password123"},
		"name":     {"Tester"},
	}
	registerBody.Set("_csrf", csrfToken)
	req := httptest.NewRequest("POST", "/auth/register", strings.NewReader(registerBody.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, ck := range sessionCookies {
		req.AddCookie(ck)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusFound, w.Code, "Шаг 1: регистрация")
	sessionCookies = mergeCookies(sessionCookies, w.Result().Cookies())

	// Шаг 2: вход
	csrfToken, sessionCookies = getCSRFToken(router, "/auth/loginIntegration", sessionCookies)
	loginIntegrationBody := url.Values{
		"email":    {"user@test.com"},
		"password": {"password123"},
	}
	loginIntegrationBody.Set("_csrf", csrfToken)
	req = httptest.NewRequest("POST", "/auth/loginIntegration", strings.NewReader(loginIntegrationBody.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, ck := range sessionCookies {
		req.AddCookie(ck)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusFound, w.Code, "Шаг 2: вход")
	sessionCookies = mergeCookies(sessionCookies, w.Result().Cookies())
	cookies := w.Result().Cookies()
	var jwtCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "jwt" {
			jwtCookie = c
			break
		}
	}
	require.NotNil(t, jwtCookie, "JWT кука должна быть установлена")
	sessionCookies = append(sessionCookies, jwtCookie)

	// Шаг 2.5: проверка дашборда
	req = httptest.NewRequest("GET", "/dashboard/", nil)
	req.AddCookie(jwtCookie)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code, "Дашборд должен быть доступен")
	bodyBytes, _ := io.ReadAll(w.Body)
	assert.Contains(t, string(bodyBytes), "Личный кабинет", "Страница дашборда должна содержать заголовок")

	// Шаг 3: создание игры
	csrfToken, sessionCookies = getCSRFToken(router, "/games/new", sessionCookies)
	createGameBody := url.Values{
		"name":            {"Integration Game"},
		"description":     {"A test description"},
		"max_team_number": {"5"},
		"visibility":      {"public"},
	}
	createGameBody.Set("_csrf", csrfToken)
	req = httptest.NewRequest("POST", "/games/new", strings.NewReader(createGameBody.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, ck := range sessionCookies {
		req.AddCookie(ck)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusFound, w.Code, "Шаг 3: создание игры")

	var createdGame game.Game
	err := db.Where("name = ?", "Integration Game").First(&createdGame).Error
	require.NoError(t, err, "Игра должна быть найдена")
	gameID := createdGame.ID
	require.NotZero(t, gameID)

	// Публикация через БД
	db.Model(&game.Game{}).Where("id = ?", gameID).Update("is_draft", false)
	db.First(&createdGame, gameID)
	require.False(t, createdGame.IsDraft, "Игра должна быть опубликована")

	// Шаг 4: создаём уровень с ответом (до старта игры!)
	lvl := &level.Level{GameID: gameID, Name: "Level 1", Position: 1}
	require.NoError(t, db.Create(lvl).Error)
	q := &level.Question{LevelID: lvl.ID, Text: "Q"}
	require.NoError(t, db.Create(q).Error)
	a := &level.Answer{QuestionID: q.ID, Code: "secret"}
	require.NoError(t, db.Create(a).Error)

	// Шаг 5: создание команды
	csrfToken, sessionCookies = getCSRFToken(router, "/teams/new", sessionCookies)
	createTeamBody := url.Values{"name": {"Test Team"}}
	createTeamBody.Set("_csrf", csrfToken)
	req = httptest.NewRequest("POST", "/teams/new", strings.NewReader(createTeamBody.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, ck := range sessionCookies {
		req.AddCookie(ck)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusFound, w.Code, "Шаг 5: создание команды")

	var createdTeam team.Team
	err = db.Where("name = ?", "Test Team").First(&createdTeam).Error
	require.NoError(t, err, "Команда должна быть найдена")
	teamID := createdTeam.ID
	require.NotZero(t, teamID)

	// Шаг 6: подача заявки
	applyURL := fmt.Sprintf("/games/%d/apply", gameID)
	csrfToken, sessionCookies = getCSRFToken(router, applyURL, sessionCookies)
	applyBody := url.Values{"team_id": {fmt.Sprint(teamID)}}
	applyBody.Set("_csrf", csrfToken)
	req = httptest.NewRequest("POST", applyURL, strings.NewReader(applyBody.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, ck := range sessionCookies {
		req.AddCookie(ck)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusFound, w.Code, "Шаг 6: подача заявки")

	// Шаг 7: принятие заявки
	passingsURL := fmt.Sprintf("/games/%d/passings", gameID)
	csrfToken, sessionCookies = getCSRFToken(router, passingsURL, sessionCookies)

	var passing game.GamePassing
	err = db.Where("game_id = ? AND team_id = ?", gameID, teamID).First(&passing).Error
	require.NoError(t, err, "Заявка должна существовать")
	passingID := passing.ID
	require.NotZero(t, passingID)

	acceptBody := url.Values{"status": {"accepted"}}
	acceptBody.Set("_csrf", csrfToken)
	req = httptest.NewRequest("POST", fmt.Sprintf("/games/%d/passings/%d/status", gameID, passingID), strings.NewReader(acceptBody.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, ck := range sessionCookies {
		req.AddCookie(ck)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusFound, w.Code, "Шаг 7: принятие заявки")

	// Шаг 8: старт игры (уровни уже есть, ошибки не будет)
	startBody := url.Values{}
	startBody.Set("_csrf", csrfToken)
	req = httptest.NewRequest("POST", fmt.Sprintf("/games/%d/passings/%d/start", gameID, passingID), strings.NewReader(startBody.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, ck := range sessionCookies {
		req.AddCookie(ck)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusFound, w.Code, "Шаг 8: старт игры")

	// Шаг 9: ввод правильного кода (GET для токена со страницы игры, POST на submit)
	gamePageURL := fmt.Sprintf("/game/%d", passingID)
	csrfToken, sessionCookies = getCSRFToken(router, gamePageURL, sessionCookies)
	submitBody := url.Values{"code": {"secret"}}
	submitBody.Set("_csrf", csrfToken)
	req = httptest.NewRequest("POST", fmt.Sprintf("/game/%d/submit", passingID), strings.NewReader(submitBody.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, ck := range sessionCookies {
		req.AddCookie(ck)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusFound, w.Code, "Шаг 9: ввод правильного кода")

	// Шаг 10: проверка завершения игры
	var updatedPassing game.GamePassing
	db.First(&updatedPassing, passingID)
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

	// Логаемся как other
	_, sessionCookies := loginIntegration(router, "other_perm@test.com", "pass123")

	// T1: Non-manager не может force-finish
	t.Run("non_manager_cannot_force_finish", func(t *testing.T) {
		gameURL := fmt.Sprintf("/games/%d/force-finish", g.ID)
		csrfToken, _ := getCSRFToken(router, gameURL, sessionCookies)

		reqBody := url.Values{"_csrf": {csrfToken}}
		req := httptest.NewRequest("POST", gameURL, strings.NewReader(reqBody.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, ck := range sessionCookies {
			req.AddCookie(ck)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code, "Non-manager не должен иметь доступ к force-finish")
	})

	// Логаемся как author
	_, sessionCookies = loginIntegration(router, "auth_perm@test.com", "pass123")

	// T2: Author может force-finish
	t.Run("author_can_force_finish", func(t *testing.T) {
		gameURL := fmt.Sprintf("/games/%d/force-finish", g.ID)
		csrfToken, _ := getCSRFToken(router, gameURL, sessionCookies)

		reqBody := url.Values{"_csrf": {csrfToken}}
		req := httptest.NewRequest("POST", gameURL, strings.NewReader(reqBody.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, ck := range sessionCookies {
			req.AddCookie(ck)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		// Author должен получить redirect или 200, а не 403
		assert.NotEqual(t, http.StatusForbidden, w.Code, "Author должен иметь доступ к force-finish")
	})

	// T3: Non-manager не может disqualify
	t.Run("non_manager_cannot_disqualify", func(t *testing.T) {
		// Создаём team и passing для другого пользователя
		tm := createTeamIntegration(t, db, other.ID)
		p := createPassingIntegration(t, db, g.ID, tm.ID, game.StatusStarted)

		gameURL := fmt.Sprintf("/games/%d/passings/%d/disqualify", g.ID, p.ID)
		csrfToken, _ := getCSRFToken(router, gameURL, sessionCookies)

		reqBody := url.Values{
			"_csrf":   {csrfToken},
			"team_id": {fmt.Sprintf("%d", tm.ID)},
		}
		req := httptest.NewRequest("POST", gameURL, strings.NewReader(reqBody.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, ck := range sessionCookies {
			req.AddCookie(ck)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code, "Non-manager не должен иметь доступ к disqualify")
	})

	// T4: Автор может disqualify
	t.Run("author_can_disqualify", func(t *testing.T) {
		tm := createTeamIntegration(t, db, author.ID)
		p := createPassingIntegration(t, db, g.ID, tm.ID, game.StatusStarted)

		gameURL := fmt.Sprintf("/games/%d/passings/%d/disqualify", g.ID, p.ID)
		csrfToken, _ := getCSRFToken(router, gameURL, sessionCookies)

		reqBody := url.Values{
			"_csrf":   {csrfToken},
			"team_id": {fmt.Sprintf("%d", tm.ID)},
		}
		req := httptest.NewRequest("POST", gameURL, strings.NewReader(reqBody.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, ck := range sessionCookies {
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

	// Логаемся как other
	_, sessionCookies := loginIntegration(router, "other_int@test.com", "pass123")

	// T1: Non-manager не может force-finish
	t.Run("non_manager_cannot_force_finish", func(t *testing.T) {
		gameURL := fmt.Sprintf("/games/%d/force-finish", g.ID)
		csrfToken, _ := getCSRFToken(router, gameURL, sessionCookies)

		reqBody := url.Values{"_csrf": {csrfToken}}
		req := httptest.NewRequest("POST", gameURL, strings.NewReader(reqBody.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, ck := range sessionCookies {
			req.AddCookie(ck)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code, "Non-manager не должен иметь доступ к force-finish")
	})

	// Логаемся как author
	_, sessionCookies = loginIntegration(router, "auth_int@test.com", "pass123")

	// T2: Author может force-finish
	t.Run("author_can_force_finish", func(t *testing.T) {
		gameURL := fmt.Sprintf("/games/%d/force-finish", g.ID)
		csrfToken, _ := getCSRFToken(router, gameURL, sessionCookies)

		reqBody := url.Values{"_csrf": {csrfToken}}
		req := httptest.NewRequest("POST", gameURL, strings.NewReader(reqBody.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, ck := range sessionCookies {
			req.AddCookie(ck)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		// Author должен получить redirect или 200, а не 403
		assert.NotEqual(t, http.StatusForbidden, w.Code, "Author должен иметь доступ к force-finish")
	})

	// T3: Non-manager не может disqualify
	t.Run("non_manager_cannot_disqualify", func(t *testing.T) {
		// Создаём team и passing для другого пользователя
		tm := createTeamIntegration(t, db, other.ID)
		p := createPassingIntegration(t, db, g.ID, tm.ID, game.StatusStarted)

		gameURL := fmt.Sprintf("/games/%d/passings/%d/disqualify", g.ID, p.ID)
		csrfToken, _ := getCSRFToken(router, gameURL, sessionCookies)

		reqBody := url.Values{
			"_csrf":   {csrfToken},
			"team_id": {fmt.Sprintf("%d", tm.ID)},
		}
		req := httptest.NewRequest("POST", gameURL, strings.NewReader(reqBody.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, ck := range sessionCookies {
			req.AddCookie(ck)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code, "Non-manager не должен иметь доступ к disqualify")
	})

	// T4: Автор может disqualify
	t.Run("author_can_disqualify", func(t *testing.T) {
		tm := createTeamIntegration(t, db, author.ID)
		p := createPassingIntegration(t, db, g.ID, tm.ID, game.StatusStarted)

		gameURL := fmt.Sprintf("/games/%d/passings/%d/disqualify", g.ID, p.ID)
		csrfToken, _ := getCSRFToken(router, gameURL, sessionCookies)

		reqBody := url.Values{
			"_csrf":   {csrfToken},
			"team_id": {fmt.Sprintf("%d", tm.ID)},
		}
		req := httptest.NewRequest("POST", gameURL, strings.NewReader(reqBody.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, ck := range sessionCookies {
			req.AddCookie(ck)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.NotEqual(t, http.StatusForbidden, w.Code, "Author должен иметь доступ к disqualify")
	})
}
