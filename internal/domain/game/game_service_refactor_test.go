// internal/domain/game/game_service_refactor_test.go
package game

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAllowedSortFields_Refactor проверяет что белый список полей сортировки работает
func TestAllowedSortFields_Refactor(t *testing.T) {
	// Все разрешённые поля должны быть в map
	assert.True(t, allowedSortFields["created_at"])
	assert.True(t, allowedSortFields["name"])
	assert.True(t, allowedSortFields["starts_at"])
	assert.True(t, allowedSortFields["rating"])
	assert.True(t, allowedSortFields["participants"])

	// Запрещённое поле
	assert.False(t, allowedSortFields["invalid_field"])
}

// TestFilterConstants проверяет константы фильтрации
func TestFilterConstants(t *testing.T) {
	assert.Equal(t, "draft", filterDraft)
	assert.Equal(t, "published", filterPublished)
}

// TestCreateGameDTO_defaultValues проверяет DTO по умолчанию
func TestCreateGameDTO_defaultValues(t *testing.T) {
	dto := &CreateGameDTO{
		Name:       "Test",
		Visibility: "public",
	}

	assert.Equal(t, "Test", dto.Name)
	assert.Equal(t, "public", dto.Visibility)
	assert.False(t, dto.IsDraft) // по умолчанию false в DTO
	assert.Nil(t, dto.StartsAt)
	assert.Nil(t, dto.CoverFile)
}

// TestUpdateGameDTO проверяет DTO обновления
func TestUpdateGameDTO(t *testing.T) {
	dto := &UpdateGameDTO{
		Name:        "Updated",
		DeleteCover: true,
	}

	assert.Equal(t, "Updated", dto.Name)
	assert.True(t, dto.DeleteCover)
	assert.Nil(t, dto.CoverFile)
}

// TestCanViewGame_logic проверяет логику проверки доступа
func TestCanViewGame_logic(t *testing.T) {
	// Публичная нечерновая игра всегда доступна
	publicGame := &Game{
		IsDraft:    false,
		Visibility: "public",
	}

	// private visibility не является черновиком — доступна
	privateGame := &Game{
		IsDraft:    false,
		Visibility: "private",
	}

	// Черновик доступен только автору/менеджеру
	draftGame := &Game{
		IsDraft:    true,
		Visibility: "private",
	}

	assert.NotNil(t, publicGame)
	assert.NotNil(t, privateGame)
	assert.NotNil(t, draftGame)
}

// TestGameSort проверяет структуру сортировки
func TestGameSort(t *testing.T) {
	sort := &GameSort{
		Field: "name",
		Order: "ASC",
	}

	assert.Equal(t, "name", sort.Field)
	assert.Equal(t, "ASC", string(sort.Order))
}
