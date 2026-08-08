// internal/domain/game/service_helpers_test.go
package game

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gengine-0/internal/pkg/validation"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAllowedSortFields тестирует белый список полей сортировки
func TestAllowedSortFields(t *testing.T) {
	tests := []struct {
		field   string
		allowed bool
	}{
		{"created_at", true},
		{"name", true},
		{"starts_at", true},
		{"rating", true},
		{"participants", true},
		{"invalid_field", false},
		{"", false},
		{"author_id", false},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			_, ok := AllowedSortFields[tt.field]
			assert.Equal(t, tt.allowed, ok, "AllowedSortFields[%s] should be %v", tt.field, tt.allowed)
		})
	}
}

// TestParseGameDatesFromForm тестирует функцию парсинга дат
func TestParseGameDatesFromForm(t *testing.T) {
	tests := []struct {
		name                 string
		startsAt             string
		registrationDeadline string
		wantErr              bool
		wantStartsAt         bool
	}{
		{
			name:                 "пустые строки",
			startsAt:             "",
			registrationDeadline: "",
			wantErr:              false,
			wantStartsAt:         false,
		},
		{
			name:                 "валидные даты",
			startsAt:             "2027-01-01T10:00",
			registrationDeadline: "2026-12-31T23:59",
			wantErr:              false,
			wantStartsAt:         true,
		},
		{
			name:                 "неверный формат даты начала",
			startsAt:             "invalid",
			registrationDeadline: "2025-12-31T23:59",
			wantErr:              true,
			wantStartsAt:         false,
		},
		{
			name:                 "неверный формат дедлайна",
			startsAt:             "2026-01-01T10:00",
			registrationDeadline: "invalid",
			wantErr:              true,
			wantStartsAt:         false,
		},
		{
			name:                 "дата начала раньше регистрации",
			startsAt:             "2025-01-01T10:00",
			registrationDeadline: "2025-01-02T10:00",
			wantErr:              true,
			wantStartsAt:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// gin-контекст с cookie tz_offset=0 (UTC по умолчанию).
			gin.SetMode(gin.TestMode)
			r := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(r)
			c.Request, _ = http.NewRequest(http.MethodGet, "/", nil)

			startsAt, registrationDeadline, err := parseGameDatesFromForm(c, tt.startsAt, tt.registrationDeadline)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.wantStartsAt {
					assert.NotNil(t, startsAt)
					assert.NotNil(t, registrationDeadline)
				} else {
					assert.Nil(t, startsAt)
					assert.Nil(t, registrationDeadline)
				}
			}
		})
	}
}

// UX-1 (pass 31): parseGameDatesFromForm конвертирует локальное время из
// формы в UTC с учётом tz_offset cookie.
func TestParseGameDatesFromForm_AppliesTZOffset(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request, _ = http.NewRequest(http.MethodGet, "/", nil)
	// UTC+3 (180 минут от UTC).
	c.Request.AddCookie(&http.Cookie{Name: "tz_offset", Value: "180"})

	startsAt, _, err := parseGameDatesFromForm(c, "2027-06-01T12:00", "2027-06-15T18:00")
	require.NoError(t, err)
	require.NotNil(t, startsAt)
	// Локальное 12:00 (UTC+3) → UTC 09:00.
	assert.Equal(t, "2027-06-01 09:00:00 +0000 UTC", startsAt.UTC().Format("2006-01-02 15:04:05 -0700 MST"))
}

// TestValidateGameDates тестирует валидацию дат
func TestValidateGameDates(t *testing.T) {
	now := time.Now()
	later := now.Add(24 * time.Hour)
	muchLater := now.Add(48 * time.Hour)

	tests := []struct {
		name                 string
		startsAt             *time.Time
		registrationDeadline *time.Time
		wantErr              bool
	}{
		{
			name:                 "обе даты nil",
			startsAt:             nil,
			registrationDeadline: nil,
			wantErr:              false,
		},
		{
			name:                 "только startsAt (будущее)",
			startsAt:             &later,
			registrationDeadline: nil,
			wantErr:              false,
		},
		{
			name:                 "валидные даты (будущее)",
			startsAt:             &muchLater,
			registrationDeadline: &later,
			wantErr:              false,
		},
		{
			name:                 "registrationDeadline после startsAt (допустимо)",
			startsAt:             &later,
			registrationDeadline: &muchLater,
			wantErr:              false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validation.ValidateGameDates(tt.startsAt, tt.registrationDeadline)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
