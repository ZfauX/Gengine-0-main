// internal/domain/user/repository_integration_test.go
// Интеграционные тесты репозитория пользователей (требуют PostgreSQL;
// пропускаются при `-short`).
package user

import (
	"context"
	"errors"
	"testing"

	"gengine-0/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// H5 (pass 30): GetUserRole через First возвращает ErrRecordNotFound для
// отсутствующего пользователя — role-provider отзывает JWT удалённого.
func TestGormUserRepo_GetUserRole(t *testing.T) {
	db := testutil.SetupPostgresDB(t, &User{})
	repo := NewGormUserRepo(db)
	ctx := context.Background()

	t.Run("success returns role", func(t *testing.T) {
		u := User{Email: "role@test.com", Name: "Role User", Password: "x", Role: "admin"}
		require.NoError(t, db.Create(&u).Error)

		role, err := repo.GetUserRole(ctx, u.ID)
		require.NoError(t, err)
		assert.Equal(t, "admin", role)
	})

	t.Run("missing user returns ErrRecordNotFound", func(t *testing.T) {
		_, err := repo.GetUserRole(ctx, 999999)
		require.Error(t, err)
		assert.True(t, errors.Is(err, gorm.ErrRecordNotFound),
			"ожидался gorm.ErrRecordNotFound, получено: %v", err)
	})
}

// H5: GetGamesView возвращает "table" по умолчанию и не ошибка при отсутствии.
func TestGormUserRepo_GetGamesView(t *testing.T) {
	db := testutil.SetupPostgresDB(t, &User{})
	repo := NewGormUserRepo(db)
	ctx := context.Background()

	t.Run("default is table", func(t *testing.T) {
		u := User{Email: "view@test.com", Name: "View User", Password: "x"}
		require.NoError(t, db.Create(&u).Error)

		view, err := repo.GetGamesView(ctx, u.ID)
		require.NoError(t, err)
		assert.Equal(t, "table", view)
	})

	t.Run("missing user default table", func(t *testing.T) {
		view, err := repo.GetGamesView(ctx, 999999)
		require.NoError(t, err)
		assert.Equal(t, "table", view)
	})

	t.Run("custom view returned", func(t *testing.T) {
		u := User{Email: "view2@test.com", Name: "View2", Password: "x"}
		require.NoError(t, db.Create(&u).Error)
		require.NoError(t, db.Model(&User{}).Where("id = ?", u.ID).Update("games_view", "cards").Error)

		view, err := repo.GetGamesView(ctx, u.ID)
		require.NoError(t, err)
		assert.Equal(t, "cards", view)
	})
}
