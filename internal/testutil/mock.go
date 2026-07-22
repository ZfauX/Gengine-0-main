// internal/testutil/mock.go
package testutil

import (
	"context"

	"gorm.io/gorm"
)

// GameAuthorizerStub — заглушка для middleware.GameAuthorizer.
// Используется в тестировании доменных сервисов, которым требуется проверка прав.
// Примечание: не импортирует game.Game для избежания циклических импортов.
type GameAuthorizerStub struct {
	DB *gorm.DB
}

// IsUserManager проверяет, является ли пользователь автором или менеджером игры.
// Для тестов использует generic-подход без прямой зависимости от game.Game.
func (s *GameAuthorizerStub) IsUserManager(ctx context.Context, gameID, userID uint) (bool, error) {
	// В тестах возвращаем true для всех запросов — авторизация не тестируется здесь
	return true, nil
}

// HasPermission проверяет наличие роли у пользователя в игре.
// Для stub всегда возвращает true.
func (s *GameAuthorizerStub) HasPermission(ctx context.Context, gameID, userID uint, role string) (bool, error) {
	return true, nil
}

// NewGameAuthorizerStub создаёт новую заглушку авторизатора.
func NewGameAuthorizerStub(db *gorm.DB) *GameAuthorizerStub {
	return &GameAuthorizerStub{DB: db}
}
