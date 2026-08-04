// internal/domain/game/game_crud_view_test.go
package game_test

import (
	"context"
	"testing"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupCRUDViewTest создаёт изолированную БД и CRUD-сервис для проверки прав просмотра.
func setupCRUDViewTest(t *testing.T) (*gorm.DB, *game.GameCRUDService) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	db := testutil.SetupPostgresDB(t, allModels...)
	repo := game.NewGormGameRepo(db)
	crud := game.NewGameCRUDService(repo, game.NewCoAuthorService(db), nil, nil, nil, nil)
	return db, crud
}

// TestCanViewGame_AdminCanViewDraft проверяет, что админ может просматривать
// черновик, автором которого он не является (регрессия доступа).
func TestCanViewGame_AdminCanViewDraft(t *testing.T) {
	db, crud := setupCRUDViewTest(t)
	ctx := context.Background()

	author := createUser(t, db, "author@test.com", "pass")
	other := createUser(t, db, "other@test.com", "pass")

	g := createDraftGame(t, db, author.ID, "Admin Draft")

	// Обычный пользователь (не автор, не админ) — НЕ должен видеть черновик
	ok, err := crud.CanViewGame(ctx, g, other.ID, "user")
	require.NoError(t, err)
	assert.False(t, ok, "обычный пользователь не должен видеть чужой черновик")

	// Админ — ДОЛЖЕН видеть черновик
	ok, err = crud.CanViewGame(ctx, g, other.ID, "admin")
	require.NoError(t, err)
	assert.True(t, ok, "админ должен видеть черновик другого автора")

	// Автор — ДОЛЖЕН видеть свой черновик
	ok, err = crud.CanViewGame(ctx, g, author.ID, "user")
	require.NoError(t, err)
	assert.True(t, ok, "автор должен видеть свой черновик")
}

// TestCanViewGame_AdminCanViewPrivate проверяет приватную игру (не черновик).
func TestCanViewGame_AdminCanViewPrivate(t *testing.T) {
	db, crud := setupCRUDViewTest(t)
	ctx := context.Background()

	author := createUser(t, db, "pvt-author@test.com", "pass")
	other := createUser(t, db, "pvt-other@test.com", "pass")

	g := &game.Game{
		Name:          "Private Game",
		Description:   "Private",
		AuthorID:      author.ID,
		Visibility:    "private",
		IsDraft:       false,
		MaxTeamNumber: 10,
	}
	require.NoError(t, db.Create(g).Error)

	// Обычный пользователь — НЕ должен видеть приватную игру
	ok, err := crud.CanViewGame(ctx, g, other.ID, "user")
	require.NoError(t, err)
	assert.False(t, ok, "обычный пользователь не должен видеть чужую приватную игру")

	// Админ — ДОЛЖЕН видеть приватную игру
	ok, err = crud.CanViewGame(ctx, g, other.ID, "admin")
	require.NoError(t, err)
	assert.True(t, ok, "админ должен видеть приватную игру")
}

// TestCanViewGame_publicNonDraftAlwaysVisible проверяет публичную опубликованную игру.
func TestCanViewGame_publicNonDraftAlwaysVisible(t *testing.T) {
	db, crud := setupCRUDViewTest(t)
	ctx := context.Background()

	author := createUser(t, db, "pub-author@test.com", "pass")

	g := createPublishedGameWithSettings(t, db, author.ID, "Public Game")

	// Любой пользователь видит публичную опубликованную игру
	ok, err := crud.CanViewGame(ctx, g, 99999, "user")
	require.NoError(t, err)
	assert.True(t, ok, "публичная опубликованная игра должна быть видна всем")
}
