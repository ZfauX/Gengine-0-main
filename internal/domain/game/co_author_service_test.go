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
	if testing.Short() {
		t.Skip("skipping DB test in short mode")
	}
	db := testutil.SetupPostgresDB(t, allModels...)
	svc := game.NewCoAuthorService().WithRepository(game.NewGormCoAuthorRepo(db))
	return db, svc
}

func TestCoAuthorService_AddAndList(t *testing.T) {
	db, svc := setupCoAuthorTest(t)
	ctx := context.Background()

	author := createUser(t, db, "author@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "Test Game")
	coAuthor := createUser(t, db, "coauthor@test.com", "pass")

	// A-1 (pass 45): Add принимает роль и permissions (nil = пресет роли).
	err := svc.Add(ctx, g.ID, coAuthor.ID, author.ID, game.RoleContentEditor, nil)
	require.NoError(t, err)

	list, err := svc.List(ctx, g.ID)
	require.NoError(t, err)
	assert.Len(t, list, 1)
	assert.Equal(t, coAuthor.ID, list[0].UserID)
	// Пресет content_editor включает право на контент.
	assert.Contains(t, list[0].Permissions, game.PermEditContent)
}

func TestCoAuthorService_Remove(t *testing.T) {
	db, svc := setupCoAuthorTest(t)
	ctx := context.Background()

	author := createUser(t, db, "author@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "Test Game")
	coAuthor := createUser(t, db, "coauthor@test.com", "pass")

	err := svc.Add(ctx, g.ID, coAuthor.ID, author.ID, game.RoleContentEditor, nil)
	require.NoError(t, err)

	err = svc.Remove(ctx, g.ID, coAuthor.ID, author.ID)
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

	err := svc.Add(ctx, g.ID, coAuthor.ID, author.ID, game.RoleContentEditor, nil)
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

	// Добавляем с ролью — permissions заполняются пресетом (A-1).
	err := svc.Add(ctx, g.ID, moderator.ID, author.ID, game.RoleModerator, nil)
	require.NoError(t, err)

	err = svc.Add(ctx, g.ID, observer.ID, author.ID, game.RoleObserver, nil)
	require.NoError(t, err)

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

	err := svc.Add(ctx, g.ID, coAuthor.ID, author.ID, game.RoleModerator, nil)
	require.NoError(t, err)

	canMod, err := svc.CanModerateGame(ctx, g.ID, coAuthor.ID)
	require.NoError(t, err)
	assert.True(t, canMod)

	canMod, err = svc.CanModerateGame(ctx, g.ID, randomUser.ID)
	require.NoError(t, err)
	assert.False(t, canMod)
}

// A-1 (pass 45): выборочные права — медиа-соавтор загружает фото, но не
// редактирует контент.
func TestCoAuthorService_SelectivePermissions(t *testing.T) {
	db, svc := setupCoAuthorTest(t)
	ctx := context.Background()

	author := createUser(t, db, "author@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "Test Game")
	mediaUser := createUser(t, db, "media@test.com", "pass")
	viewer := createUser(t, db, "viewer@test.com", "pass")

	// Медиа-соавтор: только upload_media (без edit_content/moderate).
	err := svc.Add(ctx, g.ID, mediaUser.ID, author.ID, game.RoleObserver,
		[]string{game.PermRead, game.PermUploadMedia})
	require.NoError(t, err)

	canUpload, err := svc.HasPermission(ctx, g.ID, mediaUser.ID, game.RoleUploadMedia)
	require.NoError(t, err)
	assert.True(t, canUpload)

	canEdit, err := svc.HasPermission(ctx, g.ID, mediaUser.ID, game.RoleContentEditor)
	require.NoError(t, err)
	assert.False(t, canEdit)

	// Наблюдатель: только чтение.
	err = svc.Add(ctx, g.ID, viewer.ID, author.ID, game.RoleObserver, nil)
	require.NoError(t, err)

	canUpload, err = svc.HasPermission(ctx, g.ID, viewer.ID, game.RoleUploadMedia)
	require.NoError(t, err)
	assert.False(t, canUpload)
}
