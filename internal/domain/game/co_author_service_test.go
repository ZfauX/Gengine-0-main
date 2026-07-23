// internal/domain/game/co_author_service_test.go
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

func setupCoAuthorTest(t *testing.T) (*gorm.DB, *game.CoAuthorService) {
	t.Helper()
	db := testutil.SetupPostgresDB(t, allModels...)
	svc := game.NewCoAuthorService(db)
	return db, svc
}

func TestCoAuthorService_AddAndList(t *testing.T) {
	db, svc := setupCoAuthorTest(t)
	ctx := context.Background()

	author := createUser(t, db, "author@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "Test Game")
	coAuthor := createUser(t, db, "coauthor@test.com", "pass")

	err := svc.Add(g.ID, coAuthor.ID, author.ID)
	require.NoError(t, err)

	list, err := svc.List(ctx, g.ID)
	require.NoError(t, err)
	assert.Len(t, list, 1)
	assert.Equal(t, coAuthor.ID, list[0].UserID)
}

func TestCoAuthorService_Remove(t *testing.T) {
	db, svc := setupCoAuthorTest(t)
	ctx := context.Background()

	author := createUser(t, db, "author@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "Test Game")
	coAuthor := createUser(t, db, "coauthor@test.com", "pass")

	err := svc.Add(g.ID, coAuthor.ID, author.ID)
	require.NoError(t, err)

	err = svc.Remove(g.ID, coAuthor.ID, author.ID)
	require.NoError(t, err)

	list, err := svc.List(ctx, g.ID)
	require.NoError(t, err)
	assert.Len(t, list, 0)
}

func TestCoAuthorService_IsUserManager(t *testing.T) {
	db, svc := setupCoAuthorTest(t)
	ctx := context.Background()

	author := createUser(t, db, "author@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "Test Game")
	coAuthor := createUser(t, db, "coauthor@test.com", "pass")
	randomUser := createUser(t, db, "random@test.com", "pass")

	err := svc.Add(g.ID, coAuthor.ID, author.ID)
	require.NoError(t, err)

	isManager, err := svc.IsUserManager(ctx, g.ID, coAuthor.ID)
	require.NoError(t, err)
	assert.True(t, isManager)

	isManager, err = svc.IsUserManager(ctx, g.ID, randomUser.ID)
	require.NoError(t, err)
	assert.False(t, isManager)
}

func TestCoAuthorService_HasPermission(t *testing.T) {
	db, svc := setupCoAuthorTest(t)
	ctx := context.Background()

	author := createUser(t, db, "author@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "Test Game")
	moderator := createUser(t, db, "moderator@test.com", "pass")
	observer := createUser(t, db, "observer@test.com", "pass")

	err := svc.Add(g.ID, moderator.ID, author.ID)
	require.NoError(t, err)
	require.NoError(t, db.Model(&game.CoAuthor{}).
		Where("game_id = ? AND user_id = ?", g.ID, moderator.ID).
		Update("role", game.RoleModerator).Error)

	err = svc.Add(g.ID, observer.ID, author.ID)
	require.NoError(t, err)
	require.NoError(t, db.Model(&game.CoAuthor{}).
		Where("game_id = ? AND user_id = ?", g.ID, observer.ID).
		Update("role", game.RoleObserver).Error)

	hasPerm, err := svc.HasPermission(ctx, g.ID, moderator.ID, game.RoleModerator)
	require.NoError(t, err)
	assert.True(t, hasPerm)

	hasPerm, err = svc.HasPermission(ctx, g.ID, observer.ID, game.RoleModerator)
	require.NoError(t, err)
	assert.False(t, hasPerm)
}

func TestCoAuthorService_CanModerateGame(t *testing.T) {
	db, svc := setupCoAuthorTest(t)
	ctx := context.Background()

	author := createUser(t, db, "author@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "Test Game")
	coAuthor := createUser(t, db, "coauthor@test.com", "pass")
	randomUser := createUser(t, db, "random@test.com", "pass")

	err := svc.Add(g.ID, coAuthor.ID, author.ID)
	require.NoError(t, err)
	require.NoError(t, db.Model(&game.CoAuthor{}).
		Where("game_id = ? AND user_id = ?", g.ID, coAuthor.ID).
		Update("role", game.RoleModerator).Error)

	canMod, err := svc.CanModerateGame(ctx, g.ID, coAuthor.ID)
	require.NoError(t, err)
	assert.True(t, canMod)

	canMod, err = svc.CanModerateGame(ctx, g.ID, randomUser.ID)
	require.NoError(t, err)
	assert.False(t, canMod)
}
