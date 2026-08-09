// internal/domain/game/game_play_service_test.go
package game_test

import (
	"context"
	"testing"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/websocket"
	"gengine-0/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupGamePlayTest(t *testing.T) (*gorm.DB, *game.GamePlayService, *game.LevelProgressService) {
	t.Helper()
	db := testutil.SetupPostgresDB(t, allModels...)
	hub := websocket.NewRoomHub()
	hub.Run()
	t.Cleanup(hub.Stop)

	attemptSvc := game.NewAttemptService()
	progressSvc := game.NewLevelProgressService(db).WithRepository(game.NewGormLevelProgressRepo(db))
	monitorSvc := game.NewMonitorService(db).WithRepository(game.NewGormMonitorRepo(db))
	coAuthorSvc := game.NewCoAuthorService(db).WithRepository(game.NewGormCoAuthorRepo(db))

	playSvc := game.NewGamePlayService(db, attemptSvc, monitorSvc, hub, coAuthorSvc).
		WithRepository(game.NewGormGameRepo(db)).
		WithPassingRepository(game.NewGormGamePassingRepo(db))
	return db, playSvc, progressSvc
}

func createGamePlayTestData(t *testing.T, db *gorm.DB) (*game.Game, *level.Level, *game.GamePassing, *user.User) {
	t.Helper()
	author := createUser(t, db, "author@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "Play Test Game")

	// Создаём game_settings по умолчанию
	settings := game.GameSetting{
		GameID:             g.ID,
		MaxHints:           3,
		AllowHints:         true,
		HintPenaltySeconds: 300,
	}
	require.NoError(t, db.Create(&settings).Error)

	lvl := createLevelWithAnswer(t, db, g.ID, "Level 1", 1, "secret")
	_ = createLevel(t, db, g.ID, "Level 2", 2)

	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)
	createLevelProgress(t, db, passing.ID, lvl.ID, false)

	return g, lvl, passing, author
}

func TestGamePlayService_SubmitCode_Success(t *testing.T) {
	db, playSvc, progressSvc := setupGamePlayTest(t)
	_, lvl, passing, author := createGamePlayTestData(t, db)

	// Проверяем начальный прогресс
	progress, err := progressSvc.GetCurrentProgress(context.Background(), passing.ID)
	require.NoError(t, err)
	assert.Equal(t, lvl.ID, progress.LevelID)

	// Отправляем правильный код
	attempt, err := playSvc.SubmitCode(context.Background(), passing.ID, author.ID, "secret")
	require.NoError(t, err)
	assert.True(t, attempt.Attempt.Success)

	// Проверяем, что прогресс переключился на следующий уровень
	newProgress, err := progressSvc.GetCurrentProgress(context.Background(), passing.ID)
	require.NoError(t, err)
	assert.NotEqual(t, lvl.ID, newProgress.LevelID)
}

func TestGamePlayService_SubmitCode_Wrong(t *testing.T) {
	db, playSvc, progressSvc := setupGamePlayTest(t)
	_, lvl, passing, author := createGamePlayTestData(t, db)

	progress, err := progressSvc.GetCurrentProgress(context.Background(), passing.ID)
	require.NoError(t, err)
	assert.Equal(t, lvl.ID, progress.LevelID)

	attempt, err := playSvc.SubmitCode(context.Background(), passing.ID, author.ID, "wrong")
	require.NoError(t, err)
	assert.False(t, attempt.Attempt.Success)

	updatedProgress, err := progressSvc.GetCurrentProgress(context.Background(), passing.ID)
	require.NoError(t, err)
	assert.Nil(t, updatedProgress.FinishedAt)
	assert.Equal(t, lvl.ID, updatedProgress.LevelID)
}

func TestGamePlayService_SubmitCode_Blackbox(t *testing.T) {
	db, playSvc, progressSvc := setupGamePlayTest(t)
	author := createUser(t, db, "author@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "Blackbox Test")
	lvl := createBlackboxLevel(t, db, g.ID, "BB", 1)

	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)
	createLevelProgress(t, db, passing.ID, lvl.ID, false)

	attempt, err := playSvc.SubmitCode(context.Background(), passing.ID, author.ID, "any")
	require.NoError(t, err)
	assert.False(t, attempt.Attempt.Success)

	progress, err := progressSvc.GetCurrentProgress(context.Background(), passing.ID)
	require.NoError(t, err)
	assert.Nil(t, progress.FinishedAt)

	err = playSvc.AcceptBlackboxAnswer(context.Background(), passing.ID, author.ID)
	require.NoError(t, err)

	_, err = progressSvc.GetCurrentProgress(context.Background(), passing.ID)
	assert.Error(t, err)
	assert.Equal(t, "нет активного уровня", err.Error())
}

func TestGamePlayService_SubmitFile(t *testing.T) {
	db, playSvc, _ := setupGamePlayTest(t)
	author := createUser(t, db, "author2@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "File Upload Game")

	// Создаём файл_upload уровень
	lvl := createLevel(t, db, g.ID, "File Level", 1)
	db.Model(&lvl).Update("type", "file_upload")

	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)
	createLevelProgress(t, db, passing.ID, lvl.ID, false)

	filePath := "/uploads/answers/test.txt"
	attempt, err := playSvc.SubmitFile(context.Background(), passing.ID, author.ID, filePath)
	require.NoError(t, err)
	assert.True(t, attempt.IsFile)
	assert.Equal(t, filePath, attempt.FilePath)
	assert.False(t, attempt.Success)
}

func TestGamePlayService_UseHint(t *testing.T) {
	db, playSvc, progressSvc := setupGamePlayTest(t)
	_, lvl, passing, author := createGamePlayTestData(t, db)

	// Добавляем hint к вопросу уровня — иначе UseHint вернёт «нет подсказки».
	require.NoError(t, db.Model(&level.Question{}).Where("level_id = ?", lvl.ID).
		Update("hint", "Подсказка").Error)

	progress, err := progressSvc.GetCurrentProgress(context.Background(), passing.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, progress.HintsUsed)
	assert.Equal(t, 0, progress.PenaltySeconds)

	hint, err := playSvc.UseHint(context.Background(), passing.ID, author.ID)
	require.NoError(t, err)
	assert.Equal(t, "Подсказка", hint)

	updatedProgress, err := progressSvc.GetCurrentProgress(context.Background(), passing.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, updatedProgress.HintsUsed)
	assert.Equal(t, 300, updatedProgress.PenaltySeconds)

	_, err = playSvc.UseHint(context.Background(), passing.ID, author.ID)
	require.NoError(t, err)

	updatedProgress, err = progressSvc.GetCurrentProgress(context.Background(), passing.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, updatedProgress.HintsUsed)
	assert.Equal(t, 300+300, updatedProgress.PenaltySeconds)
}

func TestGamePlayService_UseHint_Disabled(t *testing.T) {
	db, playSvc, _ := setupGamePlayTest(t)
	_, _, passing, author := createGamePlayTestData(t, db)

	var g game.Game
	db.First(&g, passing.GameID)
	var settings game.GameSetting
	err := db.Where("game_id = ?", g.ID).First(&settings).Error
	require.NoError(t, err)
	settings.AllowHints = false
	err = db.Save(&settings).Error
	require.NoError(t, err)

	_, err = playSvc.UseHint(context.Background(), passing.ID, author.ID)
	assert.Error(t, err)
	assert.Equal(t, "подсказки запрещены", err.Error())
}

func TestGamePlayService_UseHint_MaxReached(t *testing.T) {
	db, playSvc, _ := setupGamePlayTest(t)
	_, lvl, passing, author := createGamePlayTestData(t, db)

	require.NoError(t, db.Model(&level.Question{}).Where("level_id = ?", lvl.ID).
		Update("hint", "Подсказка").Error)

	var g game.Game
	db.First(&g, passing.GameID)
	var settings game.GameSetting
	err := db.Where("game_id = ?", g.ID).First(&settings).Error
	require.NoError(t, err)
	settings.MaxHints = 1
	err = db.Save(&settings).Error
	require.NoError(t, err)

	_, err = playSvc.UseHint(context.Background(), passing.ID, author.ID)
	require.NoError(t, err)

	_, err = playSvc.UseHint(context.Background(), passing.ID, author.ID)
	assert.Error(t, err)
	assert.Equal(t, "лимит подсказок исчерпан", err.Error())
}

func TestGamePlayService_StartTesting(t *testing.T) {
	db, playSvc, _ := setupGamePlayTest(t)
	author := createUser(t, db, "author@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "Test Game")
	_ = createLevel(t, db, g.ID, "L1", 1)
	_ = createLevel(t, db, g.ID, "L2", 2)

	passing, err := playSvc.StartTesting(context.Background(), g.ID, author.ID)
	require.NoError(t, err)
	assert.Equal(t, game.StatusTesting, passing.Status)

	var progress game.LevelProgress
	err = db.Where("game_passing_id = ?", passing.ID).First(&progress).Error
	require.NoError(t, err)
	assert.Equal(t, uint(1), progress.LevelID)
}

func TestGamePlayService_SubmitTestCode(t *testing.T) {
	db, playSvc, progressSvc := setupGamePlayTest(t)
	author := createUser(t, db, "author@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "Test Code")
	lvl := createLevelWithAnswer(t, db, g.ID, "L1", 1, "secret")

	passing, err := playSvc.StartTesting(context.Background(), g.ID, author.ID)
	require.NoError(t, err)

	attempt, err := playSvc.SubmitTestCode(context.Background(), passing.ID, author.ID, "anything")
	require.NoError(t, err)
	assert.True(t, attempt.Success)

	// Проверяем, что прогресс для уровня lvl завершён
	var progress game.LevelProgress
	err = db.Where("game_passing_id = ? AND level_id = ?", passing.ID, lvl.ID).First(&progress).Error
	require.NoError(t, err)
	assert.NotNil(t, progress.FinishedAt)

	// Проверяем, что активных прогрессов нет
	_, err = progressSvc.GetCurrentProgress(context.Background(), passing.ID)
	assert.Error(t, err)
}

func TestGamePlayService_SkipLevelTest(t *testing.T) {
	db, playSvc, _ := setupGamePlayTest(t)
	author := createUser(t, db, "author@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "Skip Test")
	l1 := createLevel(t, db, g.ID, "L1", 1)
	l2 := createLevel(t, db, g.ID, "L2", 2)

	passing, err := playSvc.StartTesting(context.Background(), g.ID, author.ID)
	require.NoError(t, err)

	var progress game.LevelProgress
	err = db.Where("game_passing_id = ? AND finished_at IS NULL", passing.ID).First(&progress).Error
	require.NoError(t, err)
	assert.Equal(t, l1.ID, progress.LevelID)

	err = playSvc.SkipLevelTest(context.Background(), passing.ID, author.ID)
	require.NoError(t, err)

	var nextProgress game.LevelProgress
	err = db.Where("game_passing_id = ? AND finished_at IS NULL", passing.ID).First(&nextProgress).Error
	require.NoError(t, err)
	assert.Equal(t, l2.ID, nextProgress.LevelID)
}

// G1: SubmitTestCode не должен работать по реальному прохождению (StatusStarted) —
// иначе автор мог бы создать Attempt{Success:true} и завершить уровень реальной команды.
func TestGamePlayService_SubmitTestCode_RejectsRealPassing(t *testing.T) {
	db, playSvc, _ := setupGamePlayTest(t)
	author := createUser(t, db, "author-g1@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "G1 Test")
	lvl := createLevelWithAnswer(t, db, g.ID, "L1", 1, "secret")

	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)
	createLevelProgress(t, db, passing.ID, lvl.ID, false)

	_, err := playSvc.SubmitTestCode(context.Background(), passing.ID, author.ID, "anything")
	require.Error(t, err)

	// Реальный прогресс не должен быть завершён.
	var progress game.LevelProgress
	err = db.Where("game_passing_id = ? AND level_id = ?", passing.ID, lvl.ID).First(&progress).Error
	require.NoError(t, err)
	assert.Nil(t, progress.FinishedAt)
}

// G1: SkipLevelTest не должен работать по реальному прохождению (StatusStarted) —
// иначе автор мог бы пропустить уровень реальной команды.
func TestGamePlayService_SkipLevelTest_RejectsRealPassing(t *testing.T) {
	db, playSvc, _ := setupGamePlayTest(t)
	author := createUser(t, db, "author-g1b@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "G1 Skip")
	l1 := createLevel(t, db, g.ID, "L1", 1)
	_ = createLevel(t, db, g.ID, "L2", 2)

	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)
	createLevelProgress(t, db, passing.ID, l1.ID, false)

	err := playSvc.SkipLevelTest(context.Background(), passing.ID, author.ID)
	require.Error(t, err)

	// Активный прогресс остаётся на L1.
	var progress game.LevelProgress
	err = db.Where("game_passing_id = ? AND finished_at IS NULL", passing.ID).First(&progress).Error
	require.NoError(t, err)
	assert.Equal(t, l1.ID, progress.LevelID)
}

// TestGamePlayService_SubmitCode_FinishesLastLevel: прохождение всех уровней
// завершает игру, ставит статус Finished и вызывает onGameFinished (T-H5).
func TestGamePlayService_SubmitCode_FinishesLastLevel(t *testing.T) {
	db, playSvc, _ := setupGamePlayTest(t)
	ctx := context.Background()

	author := createUser(t, db, "fin@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "Finish Game")

	l1 := createLevelWithAnswer(t, db, g.ID, "L1", 1, "a1")
	createLevelWithAnswer(t, db, g.ID, "L2", 2, "a2")
	createLevelWithAnswer(t, db, g.ID, "L3", 3, "a3")

	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)
	createLevelProgress(t, db, passing.ID, l1.ID, false)

	finishedCalled := false
	playSvc.WithGameFinishedCallback(func(_ context.Context, gameID uint) {
		finishedCalled = true
	})

	_, err := playSvc.SubmitCode(ctx, passing.ID, author.ID, "a1")
	require.NoError(t, err)
	_, err = playSvc.SubmitCode(ctx, passing.ID, author.ID, "a2")
	require.NoError(t, err)
	_, err = playSvc.SubmitCode(ctx, passing.ID, author.ID, "a3")
	require.NoError(t, err)

	require.NoError(t, db.First(passing, passing.ID).Error)
	assert.Equal(t, game.StatusFinished, passing.Status)
	assert.True(t, finishedCalled, "onGameFinished должен сработать на последнем уровне")

	// Прогрессов больше нет (все пройдены).
	var remaining int64
	require.NoError(t, db.Model(&game.LevelProgress{}).Where("game_passing_id = ? AND finished_at IS NULL", passing.ID).Count(&remaining).Error)
	assert.Equal(t, int64(0), remaining)
}
