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

	router := app.SetupRouter(db, localStorage, hub, cfg, "../..")
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
			Secret:       "integration-secret",
			AccessExpiry: 24 * time.Hour,
		},
		Session: config.SessionConfig{
			Secret: "test-session-secret",
		},
		SMTP: config.SMTPConfig{
			Enabled: false,
		},
	}

	db := testutil.SetupPostgresDB(t,
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

	// Шаг 1: регистрация
	registerBody := url.Values{
		"email":    {"user@test.com"},
		"password": {"password123"},
		"name":     {"Tester"},
	}
	if csrfToken != "" {
		registerBody.Set("_csrf", csrfToken)
	}
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
	csrfToken, sessionCookies = getCSRFToken(router, "/auth/login", sessionCookies)
	loginBody := url.Values{
		"email":    {"user@test.com"},
		"password": {"password123"},
	}
	if csrfToken != "" {
		loginBody.Set("_csrf", csrfToken)
	}
	req = httptest.NewRequest("POST", "/auth/login", strings.NewReader(loginBody.Encode()))
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
	req = httptest.NewRequest("GET", "/dashboard", nil)
	req.AddCookie(jwtCookie)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code, "Дашборд должен быть доступен")
	bodyBytes, _ := io.ReadAll(w.Body)
	assert.Contains(t, string(bodyBytes), "Личный кабинет", "Страница дашборда должна содержать заголовок")

	// Шаг 3: создание игры
	csrfToken, sessionCookies = getCSRFToken(router, "/games/new", sessionCookies)
	createGameBody := url.Values{
		"name":        {"Integration Game"},
		"description": {"A test"},
	}
	if csrfToken != "" {
		createGameBody.Set("_csrf", csrfToken)
	}
	req = httptest.NewRequest("POST", "/games", strings.NewReader(createGameBody.Encode()))
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

	// Шаг 5: создание команды
	csrfToken, sessionCookies = getCSRFToken(router, "/teams/new", sessionCookies)
	createTeamBody := url.Values{"name": {"Test Team"}}
	if csrfToken != "" {
		createTeamBody.Set("_csrf", csrfToken)
	}
	req = httptest.NewRequest("POST", "/teams", strings.NewReader(createTeamBody.Encode()))
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
	if csrfToken != "" {
		applyBody.Set("_csrf", csrfToken)
	}
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
	if csrfToken != "" {
		acceptBody.Set("_csrf", csrfToken)
	}
	req = httptest.NewRequest("POST", fmt.Sprintf("/games/%d/passings/%d/status", gameID, passingID), strings.NewReader(acceptBody.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, ck := range sessionCookies {
		req.AddCookie(ck)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusFound, w.Code, "Шаг 7: принятие заявки")

	// Шаг 8: старт игры
	startBody := url.Values{}
	if csrfToken != "" {
		startBody.Set("_csrf", csrfToken)
	}
	req = httptest.NewRequest("POST", fmt.Sprintf("/games/%d/passings/%d/start", gameID, passingID), strings.NewReader(startBody.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, ck := range sessionCookies {
		req.AddCookie(ck)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusFound, w.Code, "Шаг 8: старт игры")

	// Шаг 9: создаём уровень с ответом
	lvl := &level.Level{GameID: gameID, Name: "Level 1", Position: 1}
	require.NoError(t, db.Create(lvl).Error)
	q := &level.Question{LevelID: lvl.ID, Text: "Q"}
	require.NoError(t, db.Create(q).Error)
	a := &level.Answer{QuestionID: q.ID, Code: "secret"}
	require.NoError(t, db.Create(a).Error)

	// Шаг 10: инициализируем первый уровень
	require.NoError(t, game.NewLevelProgressService(db).InitFirstLevel(passingID))

	// Шаг 11: ввод правильного кода
	gameURL := fmt.Sprintf("/game/%d", passingID)
	csrfToken, sessionCookies = getCSRFToken(router, gameURL, sessionCookies)
	submitBody := url.Values{"code": {"secret"}}
	if csrfToken != "" {
		submitBody.Set("_csrf", csrfToken)
	}
	req = httptest.NewRequest("POST", gameURL, strings.NewReader(submitBody.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, ck := range sessionCookies {
		req.AddCookie(ck)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusFound, w.Code, "Шаг 11: ввод правильного кода")

	// Шаг 12: проверка завершения игры
	var updatedPassing game.GamePassing
	db.First(&updatedPassing, passingID)
	assert.Equal(t, game.StatusFinished, updatedPassing.Status, "Игра должна быть завершена")
}
