package middleware

import (
	"context"
)

// TokenParser реализуется сервисом, умеющим проверять JWT.
// Уже объявлен в auth.go, поэтому здесь не дублируем.

// GameAuthorizer проверяет права пользователя на игру.
type GameAuthorizer interface {
	IsUserManager(ctx context.Context, gameID, userID uint) (bool, error)
	// CanManageContent (H2, PASS-22): проверка, что пользователь имеет роль
	// НЕ ниже content_editor (а не observer). GameManager навешен на
	// мутирующие эндпоинты — observer (read-only) не должен их менять.
	CanManageContent(ctx context.Context, gameID, userID uint) (bool, error)
	// HasPermission (M3, PASS-22): проверка конкретной роли (с учётом jsonb
	// Permissions). Используется RequirePermission; requiredRole больше не
	// игнорируется.
	HasPermission(ctx context.Context, gameID, userID uint, requiredRole string) (bool, error)
}

// TeamAccessChecker проверяет права пользователя на управление командой.
type TeamAccessChecker interface {
	CanManageTeam(teamID, userID uint) bool
}
