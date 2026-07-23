package game_test

import (
	"testing"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupLevelProgressTest(t *testing.T) *gorm.DB {
	t.Helper()
	return testutil.SetupPostgresDB(t, allModels...)
}

func createGameWithLevels(t *testing.T, db *gorm.DB, authorID uint, levelCount int) (*game.Game, []*level.Level) {
	t.Helper()
	g := createPublishedGameWithSettings(t, db, authorID, "Level Progress Test Game")
	levels := make([]*level.Level, levelCount)
	for i := 0; i < levelCount; i++ {
		levels[i] = createLevel(t, db, g.ID, "Level", i+1)
	}
	return g, levels
}

func TestGetCurrentProgressForUpdate(t *testing.T) {
	db := setupLevelProgressTest(t)
	author := createUser(t, db, "author@test.com", "pass")
	g, levels := createGameWithLevels(t, db, author.ID, 1)
	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)
	createLevelProgress(t, db, passing.ID, levels[0].ID, false)

	progress, err := game.GetCurrentProgressForUpdate(db, passing.ID)
	require.NoError(t, err)
	require.NotNil(t, progress)
	assert.Equal(t, levels[0].ID, progress.LevelID)
	assert.Nil(t, progress.FinishedAt)
}

func TestCompleteLevel_SingleLevel(t *testing.T) {
	db := setupLevelProgressTest(t)
	author := createUser(t, db, "author@test.com", "pass")
	g, levels := createGameWithLevels(t, db, author.ID, 1)
	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)
	createLevelProgress(t, db, passing.ID, levels[0].ID, false)

	var progress game.LevelProgress
	err := db.Where("game_passing_id = ? AND level_id = ?", passing.ID, levels[0].ID).First(&progress).Error
	require.NoError(t, err)
	assert.Nil(t, progress.FinishedAt)

	onCommit, err := game.CompleteLevel(db, &progress, nil)
	require.NoError(t, err)
	assert.Nil(t, onCommit)

	var updated game.LevelProgress
	err = db.First(&updated, progress.ID).Error
	require.NoError(t, err)
	assert.NotNil(t, updated.FinishedAt)

	var finishedPassing game.GamePassing
	db.First(&finishedPassing, passing.ID)
	assert.Equal(t, game.StatusFinished, finishedPassing.Status)
}

func TestCompleteLevel_WithOnGameFinished(t *testing.T) {
	db := setupLevelProgressTest(t)
	author := createUser(t, db, "author@test.com", "pass")
	g, levels := createGameWithLevels(t, db, author.ID, 1)
	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)
	createLevelProgress(t, db, passing.ID, levels[0].ID, false)

	var progress game.LevelProgress
	err := db.Where("game_passing_id = ? AND level_id = ?", passing.ID, levels[0].ID).First(&progress).Error
	require.NoError(t, err)

	gameFinished := false
	onCommit, err := game.CompleteLevel(db, &progress, func() { gameFinished = true })
	require.NoError(t, err)
	require.NotNil(t, onCommit)

	onCommit()
	assert.True(t, gameFinished)
}

func TestAdvanceToNextLevel(t *testing.T) {
	db := setupLevelProgressTest(t)
	author := createUser(t, db, "author@test.com", "pass")
	g, levels := createGameWithLevels(t, db, author.ID, 3)
	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)
	createLevelProgress(t, db, passing.ID, levels[0].ID, false)

	onCommit, err := game.AdvanceToNextLevel(db, passing.ID, levels[0].ID, nil)
	require.NoError(t, err)
	assert.Nil(t, onCommit)

	var nextProgress game.LevelProgress
	err = db.Where("game_passing_id = ? AND level_id = ?", passing.ID, levels[1].ID).First(&nextProgress).Error
	require.NoError(t, err)
	assert.NotNil(t, nextProgress.StartedAt)
	assert.Nil(t, nextProgress.FinishedAt)
}
