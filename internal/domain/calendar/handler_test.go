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

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

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
	endOfMonth := startOfMonth.AddDate(0, 1, -1)

	// Подготовка мока
	startsAt1 := time.Date(year, time.Month(month), 15, 10, 0, 0, 0, time.UTC)
	// Создаём игры с правильным заполнением ID через gorm.Model
	games := []game.Game{
		{
			Model:    gorm.Model{ID: 1},
			Name:     "Game 1",
			StartsAt: &startsAt1,
		},
		{
			Model:    gorm.Model{ID: 2},
			Name:     "Game 2",
			StartsAt: nil,
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
	endOfMonth := startOfMonth.AddDate(0, 1, -1)

	mockRepo.On("ListByDateRange", mock.Anything, startOfMonth, endOfMonth).Return([]game.Game{}, errors.New("db error"))

	req := httptest.NewRequest("GET", "/api/v1/calendar", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var resp map[string]string
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "db error", resp["error"])
}
