// internal/domain/calendar/handler_test.go
package calendar_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gengine-0/internal/domain/calendar"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/testutil"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// =============================================================================
// Моки для unit-тестов
// =============================================================================

// mockGameRepo — мок для game.GameRepository
type mockGameRepo struct {
	mock.Mock
}

func (m *mockGameRepo) Create(ctx context.Context, g *game.Game) error {
	args := m.Called(ctx, g)
	return args.Error(0)
}
func (m *mockGameRepo) GetByID(ctx context.Context, id uint) (*game.Game, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*game.Game), args.Error(1)
}
func (m *mockGameRepo) GetByIDPreloaded(ctx context.Context, id uint) (*game.Game, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*game.Game), args.Error(1)
}
func (m *mockGameRepo) Update(ctx context.Context, g *game.Game) error {
	args := m.Called(ctx, g)
	return args.Error(0)
}
func (m *mockGameRepo) Delete(ctx context.Context, id uint) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}
func (m *mockGameRepo) Save(ctx context.Context, g *game.Game) error {
	args := m.Called(ctx, g)
	return args.Error(0)
}
func (m *mockGameRepo) Count(ctx context.Context, query *gorm.DB) (int64, error) {
	args := m.Called(ctx, query)
	return args.Get(0).(int64), args.Error(1)
}
func (m *mockGameRepo) ListFiltered(ctx context.Context, query *gorm.DB, offset, limit int) ([]game.Game, error) {
	args := m.Called(ctx, query, offset, limit)
	return args.Get(0).([]game.Game), args.Error(1)
}
func (m *mockGameRepo) Model(ctx context.Context) *gorm.DB {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*gorm.DB)
}
func (m *mockGameRepo) DB(ctx context.Context) *gorm.DB {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*gorm.DB)
}
func (m *mockGameRepo) ListByDateRange(ctx context.Context, from, to time.Time) ([]game.Game, error) {
	args := m.Called(ctx, from, to)
	return args.Get(0).([]game.Game), args.Error(1)
}

func (m *mockGameRepo) GetPassingByUser(ctx context.Context, gameID, userID uint) (*game.GamePassing, error) {
	args := m.Called(ctx, gameID, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*game.GamePassing), args.Error(1)
}

func (m *mockGameRepo) GetFinishedPassingByGameAndTeam(ctx context.Context, gameID, teamID uint) (*game.GamePassing, error) {
	args := m.Called(ctx, gameID, teamID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*game.GamePassing), args.Error(1)
}

func (m *mockGameRepo) GetLogsByGameID(ctx context.Context, gameID uint) ([]game.Log, error) {
	args := m.Called(ctx, gameID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]game.Log), args.Error(1)
}

func (m *mockGameRepo) GetLogsByGameIDPaginated(ctx context.Context, gameID uint, page, pageSize int) ([]game.Log, int64, error) {
	args := m.Called(ctx, gameID, page, pageSize)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]game.Log), args.Get(1).(int64), args.Error(2)
}

func (m *mockGameRepo) GetGameSettingByGameID(ctx context.Context, gameID uint) (*game.GameSetting, error) {
	args := m.Called(ctx, gameID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*game.GameSetting), args.Error(1)
}

func (m *mockGameRepo) UpsertGameSetting(ctx context.Context, settings *game.GameSetting) error {
	args := m.Called(ctx, settings)
	return args.Error(0)
}

func (m *mockGameRepo) IsTeamMember(ctx context.Context, teamID, userID uint) (bool, error) {
	args := m.Called(ctx, teamID, userID)
	return args.Bool(0), args.Error(1)
}

func (m *mockGameRepo) CountActivePassings(ctx context.Context, gameID uint) (int64, error) {
	args := m.Called(ctx, gameID)
	return args.Get(0).(int64), args.Error(1)
}

func (m *mockGameRepo) CountLevelsByGame(ctx context.Context, gameID uint) (int64, error) {
	args := m.Called(ctx, gameID)
	return args.Get(0).(int64), args.Error(1)
}

func (m *mockGameRepo) CountPublished(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}

func (m *mockGameRepo) AdminListGames(ctx context.Context, query, status string, offset, limit int) ([]game.Game, int64, error) {
	args := m.Called(ctx, query, status, offset, limit)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]game.Game), args.Get(1).(int64), args.Error(2)
}

func (m *mockGameRepo) IsTeamCaptain(ctx context.Context, teamID, userID uint) (bool, error) {
	args := m.Called(ctx, teamID, userID)
	return args.Bool(0), args.Error(1)
}

// =============================================================================
// Unit-тесты с моками
// =============================================================================

func TestCalendarHandler_CalendarData(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockRepo := new(mockGameRepo)
	handler := calendar.NewCalendarHandler(mockRepo)

	router := gin.New()
	router.GET("/api/v1/calendar", handler.CalendarData)

	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	startOfMonth := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	endOfMonth := time.Date(year, time.Month(month)+1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Second)

	startsAt1 := time.Date(year, time.Month(month), 15, 10, 0, 0, 0, time.UTC)
	games := []game.Game{
		{
			Model:      gorm.Model{ID: 1},
			Name:       "Game 1",
			StartsAt:   &startsAt1,
			Visibility: "public",
		},
		{
			Model:      gorm.Model{ID: 2},
			Name:       "Game 2",
			StartsAt:   nil,
			Visibility: "public",
		},
	}

	mockRepo.On("ListByDateRange", mock.Anything, startOfMonth, endOfMonth).Return(games, nil)

	req := httptest.NewRequest("GET", "/api/v1/calendar", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, float64(year), resp["year"])
	assert.Equal(t, float64(month), resp["month"])

	events, ok := resp["events"].(map[string]interface{})
	assert.True(t, ok)

	dateStr := startsAt1.Format("2006-01-02")
	assert.Contains(t, events, dateStr)
	eventList := events[dateStr].([]interface{})
	assert.Len(t, eventList, 1)
	event := eventList[0].(map[string]interface{})
	assert.Equal(t, float64(1), event["id"])
	assert.Equal(t, "Game 1", event["name"])

	mockRepo.AssertExpectations(t)
}

func TestCalendarHandler_CalendarData_Error(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockRepo := new(mockGameRepo)
	handler := calendar.NewCalendarHandler(mockRepo)

	router := gin.New()
	router.GET("/api/v1/calendar", handler.CalendarData)

	now := time.Now()
	year := now.Year()
	month := int(now.Month())
	startOfMonth := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	endOfMonth := time.Date(year, time.Month(month)+1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Second)

	mockRepo.On("ListByDateRange", mock.Anything, startOfMonth, endOfMonth).Return([]game.Game{}, errors.New("db error"))

	req := httptest.NewRequest("GET", "/api/v1/calendar", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var resp map[string]string
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "CalendarHandler: db error", resp["error"])
}

func TestCalendarHandler_CalendarData_WithCustomYearMonth(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockRepo := new(mockGameRepo)
	handler := calendar.NewCalendarHandler(mockRepo)

	router := gin.New()
	router.GET("/api/v1/calendar", handler.CalendarData)

	year := 2025
	month := 1
	startOfMonth := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	endOfMonth := time.Date(year, time.Month(month)+1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Second)

	mockRepo.On("ListByDateRange", mock.Anything, startOfMonth, endOfMonth).Return([]game.Game{}, nil)

	req := httptest.NewRequest("GET", "/api/v1/calendar?year=2025&month=1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, float64(2025), resp["year"])
	assert.Equal(t, float64(1), resp["month"])
}

// =============================================================================
// Интеграционные тесты с реальной БД
// =============================================================================

func setupCalendarTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return testutil.SetupPostgresDB(t,
		&game.Game{},
		&user.User{},
	)
}

func createTestUserForCalendar(t *testing.T, db *gorm.DB, email string) *user.User {
	t.Helper()
	u := &user.User{Email: email, Password: "hash", Name: email}
	require.NoError(t, db.Create(u).Error)
	return u
}

func createTestGameForCalendar(t *testing.T, db *gorm.DB, authorID uint, name string, startsAt *time.Time) *game.Game {
	t.Helper()
	g := &game.Game{
		Name:       name,
		AuthorID:   authorID,
		IsDraft:    false,
		StartsAt:   startsAt,
		Visibility: "public",
	}
	require.NoError(t, db.Create(g).Error)
	return g
}

func TestCalendarHandler_Integration_CalendarData(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupCalendarTestDB(t)

	author := createTestUserForCalendar(t, db, "author@test.com")

	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	startsAt1 := time.Date(year, time.Month(month), 15, 10, 0, 0, 0, time.UTC)
	startsAt2 := time.Date(year, time.Month(month), 20, 14, 30, 0, 0, time.UTC)

	createTestGameForCalendar(t, db, author.ID, "Integration Game 1", &startsAt1)
	createTestGameForCalendar(t, db, author.ID, "Integration Game 2", &startsAt2)
	createTestGameForCalendar(t, db, author.ID, "No Date", nil)

	gameRepo := game.NewGormGameRepo(db)
	handler := calendar.NewCalendarHandler(gameRepo)

	router := gin.New()
	router.GET("/api/v1/calendar", handler.CalendarData)

	req := httptest.NewRequest("GET", "/api/v1/calendar", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, float64(year), resp["year"])
	assert.Equal(t, float64(month), resp["month"])

	events, ok := resp["events"].(map[string]interface{})
	assert.True(t, ok)

	// MED-14 (pass 29): раньше пустой результат тихо скипался — регрессии
	// ListByDateRange оставались незамеченными. Данные гарантированно созданы.
	require.NotEmpty(t, events, "в календаре должны быть события созданных игр")

	date1 := startsAt1.Format("2006-01-02")
	date2 := startsAt2.Format("2006-01-02")

	assert.Contains(t, events, date1)
	assert.Contains(t, events, date2)

	eventList1 := events[date1].([]interface{})
	assert.Len(t, eventList1, 1)
	eventList2 := events[date2].([]interface{})
	assert.Len(t, eventList2, 1)

	ev1 := eventList1[0].(map[string]interface{})
	ev2 := eventList2[0].(map[string]interface{})
	assert.Equal(t, "Integration Game 1", ev1["name"])
	assert.Equal(t, "Integration Game 2", ev2["name"])
}

func TestCalendarHandler_Integration_CalendarData_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupCalendarTestDB(t)

	gameRepo := game.NewGormGameRepo(db)
	handler := calendar.NewCalendarHandler(gameRepo)

	router := gin.New()
	router.GET("/api/v1/calendar", handler.CalendarData)

	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	req := httptest.NewRequest("GET", "/api/v1/calendar", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, float64(year), resp["year"])
	assert.Equal(t, float64(month), resp["month"])

	events, ok := resp["events"].(map[string]interface{})
	assert.True(t, ok)
	assert.Empty(t, events)
}

func TestCalendarHandler_Integration_CalendarData_OtherMonth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupCalendarTestDB(t)

	author := createTestUserForCalendar(t, db, "author2@test.com")

	targetYear := 2025
	targetMonth := 1
	startsAt := time.Date(targetYear, time.Month(targetMonth), 10, 12, 0, 0, 0, time.UTC)
	createTestGameForCalendar(t, db, author.ID, "Jan Game", &startsAt)

	gameRepo := game.NewGormGameRepo(db)
	handler := calendar.NewCalendarHandler(gameRepo)

	router := gin.New()
	router.GET("/api/v1/calendar", handler.CalendarData)

	req := httptest.NewRequest("GET", "/api/v1/calendar?year=2025&month=1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, float64(2025), resp["year"])
	assert.Equal(t, float64(1), resp["month"])

	events, ok := resp["events"].(map[string]interface{})
	assert.True(t, ok)
	// MED-14 (pass 29): вместо тихого skip — жёсткая проверка.
	require.NotEmpty(t, events, "события для января должны быть в календаре")
	assert.Contains(t, events, "2025-01-10")
	assert.Len(t, events["2025-01-10"].([]interface{}), 1)
}

// MED-13 (pass 29): экранирование iCal и sanitize host были без тестов.
func TestEscapeICalText(t *testing.T) {
	tests := []struct{ in, want string }{
		{`plain`, `plain`},
		{`a;b`, `a\;b`},
		{`a,b`, `a\,b`},
		{"line\nbreak", `line\nbreak`},
		{`back\slash`, `back\\slash`}, // один backslash → экранируется как два,
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, calendar.EscapeICalText(tt.in), "EscapeICalText(%q)", tt.in)
	}
}

func TestSanitizeHost(t *testing.T) {
	tests := []struct{ in, want string }{
		{"example.com", "example.com"},
		{"example.com:8080", "example.com:8080"},
		{"my-site_1.example", "my-site_1.example"},
		{"exa mple.com", "exaxmple.com"}, // пробел → x
		{"<script>", "xscriptx"},         // инъекция → нейтрализована
		{"", "localhost"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, calendar.SanitizeHost(tt.in), "SanitizeHost(%q)", tt.in)
	}
}
