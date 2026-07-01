// internal/domain/game/game_admin_service_test.go
package game_test

import (
	"context"
	"testing"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupGameAdminTest(t *testing.T) (*gorm.DB, *game.GameAdminService) {
	t.Helper()
	db := testutil.SetupPostgresDB(t, allModels...)
	coAuthorSvc := game.NewCoAuthorService(db)
	cfg := &config.Config{}
	adminSvc := game.NewGameAdminService(db, coAuthorSvc, cfg)
	return db, adminSvc
}

func createGameAdminTestData(t *testing.T, db *gorm.DB) (*game.Game, *game.GamePassing, *user.User) {
	t.Helper()
	author := createUser(t, db, "author@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "Admin Test Game")

	lvl := createLevel(t, db, g.ID, "Level 1", 1)
	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)
	createLevelProgress(t, db, passing.ID, lvl.ID, false)

	return g, passing, author
}

func TestGameAdminService_ForceFinishGame(t *testing.T) {
	db, adminSvc := setupGameAdminTest(t)
	g, passing, _ := createGameAdminTestData(t, db)

	otherUser := createUser(t, db, "other@test.com", "pass")
	otherTeam := createTeam(t, db, otherUser.ID)
	otherPassing := createPassing(t, db, g.ID, otherTeam.ID, game.StatusStarted)
	createLevelProgress(t, db, otherPassing.ID, createLevel(t, db, g.ID, "Level 2", 2).ID, false)

	err := adminSvc.ForceFinishGame(context.Background(), g.ID)
	require.NoError(t, err)

	var passings []game.GamePassing
	db.Where("game_id = ?", g.ID).Find(&passings)
	for _, p := range passings {
		assert.Equal(t, game.StatusFinished, p.Status)
	}

	var progresses []game.LevelProgress
	db.Where("game_passing_id = ?", passing.ID).Find(&progresses)
	for _, p := range progresses {
		assert.NotNil(t, p.FinishedAt)
	}
}

func TestGameAdminService_ForceFinishGame_NoActive(t *testing.T) {
	db, adminSvc := setupGameAdminTest(t)
	author := createUser(t, db, "author@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "No Active Game")

	err := adminSvc.ForceFinishGame(context.Background(), g.ID)
	assert.Error(t, err)
	assert.Equal(t, "нет активных прохождений", err.Error())
}

func TestGameAdminService_DisqualifyTeam(t *testing.T) {
	db, adminSvc := setupGameAdminTest(t)
	g, passing, _ := createGameAdminTestData(t, db)

	err := adminSvc.DisqualifyTeam(context.Background(), g.ID, passing.TeamID)
	require.NoError(t, err)

	var updated game.GamePassing
	db.First(&updated, passing.ID)
	assert.Equal(t, game.StatusDisqualified, updated.Status)

	var progress game.LevelProgress
	err = db.Where("game_passing_id = ?", passing.ID).First(&progress).Error
	require.NoError(t, err)
	assert.NotNil(t, progress.FinishedAt)
}

func TestGameAdminService_DisqualifyTeam_NotInGame(t *testing.T) {
	db, adminSvc := setupGameAdminTest(t)
	author := createUser(t, db, "author@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "Disq Game")
	tm := createTeam(t, db, author.ID)

	err := adminSvc.DisqualifyTeam(context.Background(), g.ID, tm.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "команда не в игре")
}

func TestGameAdminService_DeleteLevelFromActiveGame(t *testing.T) {
	db, adminSvc := setupGameAdminTest(t)
	g, passing, author := createGameAdminTestData(t, db)

	progress, err := game.GetCurrentProgress(db, passing.ID)
	require.NoError(t, err)
	levelID := progress.LevelID

	nextLevel := createLevel(t, db, g.ID, "Next Level", 2)

	err = adminSvc.DeleteLevelFromActiveGame(context.Background(), g.ID, levelID, author.ID)
	require.NoError(t, err)

	// Проверяем, что уровень удалён
	var deletedLevel level.Level
	err = db.Unscoped().First(&deletedLevel, levelID).Error
	assert.Error(t, err)
	assert.Equal(t, gorm.ErrRecordNotFound, err)

	// Проверяем, что прогресс для удалённого уровня отсутствует
	var oldProgress game.LevelProgress
	err = db.Where("game_passing_id = ? AND level_id = ?", passing.ID, levelID).First(&oldProgress).Error
	assert.Error(t, err)
	assert.Equal(t, gorm.ErrRecordNotFound, err)

	// Проверяем, что прогресс переключился на следующий уровень
	updatedProgress, err := game.GetCurrentProgress(db, passing.ID)
	require.NoError(t, err)
	assert.Equal(t, nextLevel.ID, updatedProgress.LevelID)
}

func TestGameAdminService_DeleteLevelFromActiveGame_NotAuthor(t *testing.T) {
	db, adminSvc := setupGameAdminTest(t)
	g, passing, _ := createGameAdminTestData(t, db)
	other := createUser(t, db, "other@test.com", "pass")

	progress, err := game.GetCurrentProgress(db, passing.ID)
	require.NoError(t, err)

	err = adminSvc.DeleteLevelFromActiveGame(context.Background(), g.ID, progress.LevelID, other.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "только автор или контент-менеджер")
}
