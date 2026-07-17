// internal/domain/game/service_helpers_test.go
package game

import (
	"gengine-0/internal/pkg/validation"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
			_, ok := allowedSortFields[tt.field]
			assert.Equal(t, tt.allowed, ok, "allowedSortFields[%s] should be %v", tt.field, tt.allowed)
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
			startsAt, registrationDeadline, err := parseGameDatesFromForm(tt.startsAt, tt.registrationDeadline)
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
			name:                 "registrationDeadline после startsAt",
			startsAt:             &later,
			registrationDeadline: &muchLater,
			wantErr:              true,
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
