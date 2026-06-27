// internal/domain/game/service_test.go
package game_test

import (
	"context"
	"testing"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/monitor"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/websocket"
	"gengine-0/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var allModels = []any{
	&game.Game{}, &game.GamePassing{}, &game.GameSetting{}, &game.CoAuthor{}, &game.Note{},
	&game.LevelProgress{}, &game.Attempt{},
	&monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
	&game.Log{},
	&game.PlayerRating{},
	&level.Level{}, &level.Question{}, &level.Answer{},
	&team.Team{}, &team.Invitation{},
	&user.User{}, &user.Achievement{},
}

// ---------- GameService ----------

func TestGameService_Create(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	svc := newGameService(db)

	author := createUser(t, db, "author@test.com", "pass")
	g := &game.Game{Name: "Test Game"}

	err := svc.Create(context.Background(), g, author.ID)
	require.NoError(t, err)
	assert.True(t, g.IsDraft)
	assert.Equal(t, author.ID, g.AuthorID)
}

func TestGameService_Publish(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	svc := newGameService(db)

	author := createUser(t, db, "pub@test.com", "pass")
	g := &game.Game{Name: "Game", AuthorID: author.ID, IsDraft: true}
	require.NoError(t, db.Create(g).Error)

	createLevel(t, db, g.ID, "Test Level", 1)

	err := svc.Publish(context.Background(), g.ID, author.ID)
	require.NoError(t, err)

	var updated game.Game
	db.First(&updated, g.ID)
	assert.False(t, updated.IsDraft)
}

func TestGameService_ForceFinishGame(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	svc := newGameService(db)

	author := createUser(t, db, "finish@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Finish Game")

	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)

	lvl := createLevel(t, db, g.ID, "L1", 1)
	createLevelProgress(t, db, passing.ID, lvl.ID, false)

	err := svc.ForceFinishGame(context.Background(), g.ID)
	require.NoError(t, err)

	var updated game.GamePassing
	db.First(&updated, passing.ID)
	assert.Equal(t, game.StatusFinished, updated.Status)
}

func TestGameService_DisqualifyTeam(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	svc := newGameService(db)

	author := createUser(t, db, "disq@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Disq Game")

	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)

	lvl := createLevel(t, db, g.ID, "L1", 1)
	createLevelProgress(t, db, passing.ID, lvl.ID, false)

	err := svc.DisqualifyTeam(context.Background(), g.ID, tm.ID)
	require.NoError(t, err)

	var updated game.GamePassing
	db.First(&updated, passing.ID)
	assert.Equal(t, game.StatusDisqualified, updated.Status)
}

func TestGameService_GetByID(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	svc := newGameService(db)

	author := createUser(t, db, "owner@test.com", "pass")
	other := createUser(t, db, "other@test.com", "pass")
	admin := createAdmin(t, db, "admin@test.com", "pass")

	pub := createPublishedGame(t, db, author.ID, "Public Game")
	draft := &game.Game{Name: "Draft", AuthorID: author.ID, IsDraft: true}
	require.NoError(t, db.Create(draft).Error)
	priv := &game.Game{Name: "Private", AuthorID: author.ID, IsDraft: false, Visibility: "private"}
	require.NoError(t, db.Create(priv).Error)

	t.Run("публичная игра доступна любому", func(t *testing.T) {
		g, err := svc.GetByID(context.Background(), pub.ID, other.ID)
		require.NoError(t, err)
		assert.Equal(t, pub.ID, g.ID)
	})

	t.Run("черновик виден только автору или админу", func(t *testing.T) {
		_, err := svc.GetByID(context.Background(), draft.ID, other.ID)
		assert.Error(t, err)
		assert.Equal(t, "игра не найдена", err.Error())

		g, err := svc.GetByID(context.Background(), draft.ID, author.ID)
		require.NoError(t, err)
		assert.Equal(t, draft.ID, g.ID)

		g, err = svc.GetByID(context.Background(), draft.ID, admin.ID)
		require.NoError(t, err)
		assert.Equal(t, draft.ID, g.ID)
	})

	t.Run("приватная игра видна только автору или админу", func(t *testing.T) {
		_, err := svc.GetByID(context.Background(), priv.ID, other.ID)
		assert.Error(t, err)
		assert.Equal(t, "игра не найдена", err.Error())

		g, err := svc.GetByID(context.Background(), priv.ID, author.ID)
		require.NoError(t, err)
		assert.Equal(t, priv.ID, g.ID)

		g, err = svc.GetByID(context.Background(), priv.ID, admin.ID)
		require.NoError(t, err)
		assert.Equal(t, priv.ID, g.ID)
	})
}

func TestGameService_ListFilteredPaginated(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	svc := newGameService(db)

	author := createUser(t, db, "author@test.com", "pass")
	other := createUser(t, db, "other@test.com", "pass")

	_ = createPublishedGame(t, db, author.ID, "Game A")
	g2 := &game.Game{Name: "Game B", AuthorID: author.ID, IsDraft: true}
	require.NoError(t, db.Create(g2).Error)
	_ = createPublishedGame(t, db, author.ID, "Game C")
	_ = createPublishedGame(t, db, other.ID, "Game D")

	filter := game.GameFilter{Status: "published", ViewerID: author.ID}
	sort := &game.GameSort{Field: "name", Order: game.SortAsc}
	games, total, err := svc.ListFilteredPaginated(context.Background(), filter, sort, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, games, 3)
	assert.Equal(t, "Game A", games[0].Name)

	filter = game.GameFilter{Status: "draft", ViewerID: author.ID}
	games, total, err = svc.ListFilteredPaginated(context.Background(), filter, nil, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "Game B", games[0].Name)

	filter = game.GameFilter{Search: "Game C", ViewerID: author.ID}
	games, total, err = svc.ListFilteredPaginated(context.Background(), filter, nil, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "Game C", games[0].Name)

	uid := other.ID
	filter = game.GameFilter{AuthorID: &uid, ViewerID: author.ID}
	games, total, err = svc.ListFilteredPaginated(context.Background(), filter, nil, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "Game D", games[0].Name)
}

func TestGameService_Update(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	svc := newGameService(db)

	author := createUser(t, db, "author@test.com", "pass")
	other := createUser(t, db, "other@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Old Name")

	updated := &game.Game{
		Name:          "New Name",
		Description:   "New Desc",
		MaxTeamNumber: 20,
		Visibility:    "private",
	}
	err := svc.Update(context.Background(), g.ID, updated, author.ID)
	require.NoError(t, err)

	var result game.Game
	db.First(&result, g.ID)
	assert.Equal(t, "New Name", result.Name)
	assert.Equal(t, "New Desc", result.Description)
	assert.Equal(t, 20, result.MaxTeamNumber)
	assert.Equal(t, "private", result.Visibility)

	err = svc.Update(context.Background(), g.ID, updated, other.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "только автор или контент-менеджер")
}

func TestGameService_Delete(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	svc := newGameService(db)

	author := createUser(t, db, "author@test.com", "pass")
	other := createUser(t, db, "other@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "To Delete")

	err := svc.Delete(context.Background(), g.ID, other.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "только владелец")

	err = svc.Delete(context.Background(), g.ID, author.ID)
	require.NoError(t, err)

	var deleted game.Game
	err = db.First(&deleted, g.ID).Error
	assert.Error(t, err)
	assert.Equal(t, gorm.ErrRecordNotFound, err)
}

func TestGameService_SubmitCode(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	svc := newGameService(db)

	author := createUser(t, db, "author@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Code Game")
	lvl := createLevelWithAnswer(t, db, g.ID, "L1", 1, "secret")
	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)
	_ = createLevelProgress(t, db, passing.ID, lvl.ID, false)

	attempt, err := svc.SubmitCode(context.Background(), passing.ID, author.ID, "secret")
	require.NoError(t, err)
	assert.True(t, attempt.Success)
	assert.Equal(t, "secret", attempt.Code)

	var updated game.LevelProgress
	db.Where("game_passing_id = ?", passing.ID).First(&updated)
	assert.NotNil(t, updated.FinishedAt)

	var updatedPassing game.GamePassing
	db.First(&updatedPassing, passing.ID)
	assert.Equal(t, game.StatusFinished, updatedPassing.Status)
}

func TestGameService_SubmitFile(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	svc := newGameService(db)

	author := createUser(t, db, "author@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "File Game")
	lvl := createLevel(t, db, g.ID, "L1", 1)
	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)
	_ = createLevelProgress(t, db, passing.ID, lvl.ID, false)

	attempt, err := svc.SubmitFile(context.Background(), passing.ID, author.ID, "/path/to/file")
	require.NoError(t, err)
	assert.False(t, attempt.Success)
	assert.True(t, attempt.IsFile)
	assert.Equal(t, "/path/to/file", attempt.FilePath)
}

func TestGameService_UseHint(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	svc := newGameService(db)

	author := createUser(t, db, "author@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Hint Game")
	lvl := createLevel(t, db, g.ID, "L1", 1)
	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)
	progress := createLevelProgress(t, db, passing.ID, lvl.ID, false)

	err := svc.UseHint(context.Background(), passing.ID, author.ID)
	require.NoError(t, err)

	var updated game.LevelProgress
	db.First(&updated, progress.ID)
	assert.Equal(t, 1, updated.HintsUsed)
	assert.Equal(t, 300, updated.PenaltySeconds)

	err = svc.UseHint(context.Background(), passing.ID, author.ID)
	require.NoError(t, err)
	db.First(&updated, progress.ID)
	assert.Equal(t, 2, updated.HintsUsed)
	assert.Equal(t, 300+600, updated.PenaltySeconds)

	err = svc.UseHint(context.Background(), passing.ID, author.ID)
	require.NoError(t, err)
	db.First(&updated, progress.ID)
	assert.Equal(t, 3, updated.HintsUsed)
	assert.Equal(t, 300+600+900, updated.PenaltySeconds)

	err = svc.UseHint(context.Background(), passing.ID, author.ID)
	assert.Error(t, err)
	assert.Equal(t, "лимит подсказок исчерпан", err.Error())
}

func TestGameService_UseHint_Disabled(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	svc := newGameService(db)

	author := createUser(t, db, "author@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "No Hint Game")

	// Создаём настройки с AllowHints = true (из-за особенностей GORM и default-значений)
	// и сразу обновляем allow_hints на false, чтобы гарантировать значение в БД
	setting := &game.GameSetting{
		GameID:             g.ID,
		AllowHints:         true, // временно, потом поменяем
		HintPenaltySeconds: 300,
		MaxHints:           3,
	}
	err := db.Create(setting).Error
	require.NoError(t, err)

	// Принудительно устанавливаем AllowHints = false через Update
	err = db.Model(&game.GameSetting{}).Where("game_id = ?", g.ID).Update("allow_hints", false).Error
	require.NoError(t, err)

	// Проверяем, что настройка действительно сохранилась с AllowHints = false
	var savedSetting game.GameSetting
	err = db.Where("game_id = ?", g.ID).First(&savedSetting).Error
	require.NoError(t, err)
	assert.False(t, savedSetting.AllowHints)

	lvl := createLevel(t, db, g.ID, "L1", 1)
	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)
	_ = createLevelProgress(t, db, passing.ID, lvl.ID, false)

	err = svc.UseHint(context.Background(), passing.ID, author.ID)
	assert.Error(t, err)
	assert.Equal(t, "подсказки запрещены", err.Error())
}

func TestGameService_AcceptBlackboxAnswer(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	svc := newGameService(db)

	author := createUser(t, db, "author@test.com", "pass")
	other := createUser(t, db, "other@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Blackbox Game")
	lvl := createBlackboxLevel(t, db, g.ID, "BB", 1)
	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)
	progress := createLevelProgress(t, db, passing.ID, lvl.ID, false)

	attemptSvc := game.NewAttemptService(db)
	_, _, err := attemptSvc.SubmitCode(progress, "any")
	require.NoError(t, err)

	err = svc.AcceptBlackboxAnswer(context.Background(), passing.ID, author.ID)
	require.NoError(t, err)

	var attempt game.Attempt
	db.Where("level_progress_id = ?", progress.ID).Order("created_at DESC").First(&attempt)
	assert.True(t, attempt.Success)

	var updatedProgress game.LevelProgress
	db.First(&updatedProgress, progress.ID)
	assert.NotNil(t, updatedProgress.FinishedAt)

	tm2 := createTeam(t, db, other.ID)
	passing2 := createPassing(t, db, g.ID, tm2.ID, game.StatusStarted)
	progress2 := createLevelProgress(t, db, passing2.ID, lvl.ID, false)
	_, _, err = attemptSvc.SubmitCode(progress2, "any")
	require.NoError(t, err)

	err = svc.AcceptBlackboxAnswer(context.Background(), passing2.ID, other.ID)
	assert.Error(t, err)
	assert.Equal(t, "только автор может подтвердить ответ", err.Error())
}

func TestGameService_StartTesting(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	svc := newGameService(db)

	author := createUser(t, db, "author@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Test Game")
	lvl := createLevel(t, db, g.ID, "L1", 1)

	var gameWithLevels game.Game
	err := db.Preload("Levels").First(&gameWithLevels, g.ID).Error
	require.NoError(t, err)
	require.Len(t, gameWithLevels.Levels, 1)

	passing, err := svc.StartTesting(context.Background(), g.ID, author.ID)
	require.NoError(t, err)

	db.First(&passing, passing.ID)
	assert.Equal(t, game.StatusTesting, passing.Status)
	assert.NotZero(t, passing.ID)

	var progress game.LevelProgress
	err = db.Where("game_passing_id = ?", passing.ID).First(&progress).Error
	require.NoError(t, err)
	assert.Equal(t, lvl.ID, progress.LevelID, "Прогресс должен быть создан на первом уровне")
}

func TestGameService_SubmitTestCode(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	svc := newGameService(db)

	author := createUser(t, db, "author@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Test Code")
	createLevel(t, db, g.ID, "L1", 1)

	passing, err := svc.StartTesting(context.Background(), g.ID, author.ID)
	require.NoError(t, err)

	attempt, err := svc.SubmitTestCode(context.Background(), passing.ID, author.ID, "testcode")
	require.NoError(t, err)
	assert.True(t, attempt.Success)
	assert.Equal(t, "testcode", attempt.Code)

	var progress game.LevelProgress
	db.Where("game_passing_id = ?", passing.ID).First(&progress)
	assert.NotNil(t, progress.FinishedAt)
}

func TestGameService_SkipLevelTest(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	svc := newGameService(db)

	author := createUser(t, db, "author@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Skip Game")
	l1 := createLevel(t, db, g.ID, "L1", 1)
	l2 := createLevel(t, db, g.ID, "L2", 2)

	var gameWithLevels game.Game
	err := db.Preload("Levels").First(&gameWithLevels, g.ID).Error
	require.NoError(t, err)
	require.Len(t, gameWithLevels.Levels, 2)

	passing, err := svc.StartTesting(context.Background(), g.ID, author.ID)
	require.NoError(t, err)

	// Проверяем, что прогресс создан на первом уровне
	var progress game.LevelProgress
	err = db.Where("game_passing_id = ? AND finished_at IS NULL", passing.ID).First(&progress).Error
	require.NoError(t, err)
	assert.Equal(t, l1.ID, progress.LevelID)

	err = svc.SkipLevelTest(context.Background(), passing.ID, author.ID)
	require.NoError(t, err)

	// После пропуска активный прогресс должен быть на втором уровне (новая переменная!)
	var progressAfter game.LevelProgress
	err = db.Where("game_passing_id = ? AND finished_at IS NULL", passing.ID).First(&progressAfter).Error
	require.NoError(t, err)
	assert.Equal(t, l2.ID, progressAfter.LevelID, "После пропуска первого уровня прогресс должен быть на втором")
}

func TestGameService_DeleteLevelFromActiveGame(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	svc := newGameService(db)

	author := createUser(t, db, "author@test.com", "pass")
	other := createUser(t, db, "other@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Delete Level")
	lvl1 := createLevel(t, db, g.ID, "L1", 1)
	lvl2 := createLevel(t, db, g.ID, "L2", 2)

	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)
	_ = createLevelProgress(t, db, passing.ID, lvl1.ID, false)

	// Пользователь без прав
	err := svc.DeleteLevelFromActiveGame(context.Background(), g.ID, lvl1.ID, other.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "только автор или контент-менеджер")

	// Автор удаляет первый уровень (сервис сам разрулит FK)
	err = svc.DeleteLevelFromActiveGame(context.Background(), g.ID, lvl1.ID, author.ID)
	require.NoError(t, err)

	// Проверяем, что уровень удалён
	var deleted level.Level
	err = db.Unscoped().First(&deleted, lvl1.ID).Error
	assert.Error(t, err)

	// Проверяем, что прогресс переключился на L2 (новый прогресс должен быть создан)
	var updatedProgress game.LevelProgress
	err = db.Where("game_passing_id = ?", passing.ID).First(&updatedProgress).Error
	require.NoError(t, err)
	assert.Equal(t, lvl2.ID, updatedProgress.LevelID, "Прогресс должен переключиться на следующий уровень")
}

// ---------- GamePassingService ----------

func TestGamePassingService_Apply(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	svc := newPassingService(db)

	author := createUser(t, db, "auth@test.com", "pass")
	cap := createUser(t, db, "cap@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Apply Game")
	tm := createTeamWithCaptain(t, db, cap.ID)

	err := svc.Apply(context.Background(), g.ID, tm.ID, cap.ID)
	require.NoError(t, err)

	var passing game.GamePassing
	db.Where("game_id = ? AND team_id = ?", g.ID, tm.ID).First(&passing)
	assert.Equal(t, game.StatusPending, passing.Status)
}

func TestGamePassingService_Apply_NotCaptain(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	svc := newPassingService(db)

	author := createUser(t, db, "auth2@test.com", "pass")
	other := createUser(t, db, "other@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Game")
	tm := createTeamWithCaptain(t, db, other.ID)

	err := svc.Apply(context.Background(), g.ID, tm.ID, other.ID+1)
	assert.Error(t, err)
}

func TestGamePassingService_Accept(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	svc := newPassingService(db)

	author := createUser(t, db, "author3@test.com", "pass")
	cap := createUser(t, db, "cap3@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Accept Game")
	tm := createTeamWithCaptain(t, db, cap.ID)

	require.NoError(t, svc.Apply(context.Background(), g.ID, tm.ID, cap.ID))

	var passing game.GamePassing
	require.NoError(t, db.Where("game_id = ? AND team_id = ?", g.ID, tm.ID).First(&passing).Error)

	err := svc.UpdateStatus(context.Background(), passing.ID, game.StatusAccepted, author.ID)
	require.NoError(t, err)

	db.First(&passing, passing.ID)
	assert.Equal(t, game.StatusAccepted, passing.Status)
}

func TestGamePassingService_StartGame(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	svc := newPassingService(db)

	author := createUser(t, db, "author4@test.com", "pass")
	cap := createUser(t, db, "cap4@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Start Game")
	tm := createTeamWithCaptain(t, db, cap.ID)

	require.NoError(t, svc.Apply(context.Background(), g.ID, tm.ID, cap.ID))
	var passing game.GamePassing
	require.NoError(t, db.Where("game_id = ? AND team_id = ?", g.ID, tm.ID).First(&passing).Error)
	require.NoError(t, svc.UpdateStatus(context.Background(), passing.ID, game.StatusAccepted, author.ID))

	err := svc.StartGame(context.Background(), passing.ID, cap.ID)
	require.NoError(t, err)

	db.First(&passing, passing.ID)
	assert.Equal(t, game.StatusStarted, passing.Status)
}

// ---------- AttemptService ----------

func TestAttemptService_SubmitCode_Correct(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	attemptSvc := game.NewAttemptService(db)

	author := createUser(t, db, "att@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Attempt Game")
	lvl := createLevelWithAnswer(t, db, g.ID, "L1", 1, "secret")

	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)
	progress := createLevelProgress(t, db, passing.ID, lvl.ID, false)

	attempt, success, err := attemptSvc.SubmitCode(progress, "secret")
	require.NoError(t, err)
	assert.True(t, success)
	assert.Equal(t, "secret", attempt.Code)
}

func TestAttemptService_SubmitCode_Wrong(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	attemptSvc := game.NewAttemptService(db)

	author := createUser(t, db, "wrong@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Wrong Code")
	lvl := createLevelWithAnswer(t, db, g.ID, "L1", 1, "secret")

	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)
	progress := createLevelProgress(t, db, passing.ID, lvl.ID, false)

	attempt, success, err := attemptSvc.SubmitCode(progress, "bad")
	require.NoError(t, err)
	assert.False(t, success)
	assert.Equal(t, "bad", attempt.Code)
}

func TestAttemptService_Blackbox_Pending(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	attemptSvc := game.NewAttemptService(db)

	author := createUser(t, db, "bb@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Blackbox")
	lvl := createBlackboxLevel(t, db, g.ID, "BB", 1)

	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)
	progress := createLevelProgress(t, db, passing.ID, lvl.ID, false)

	_, success, err := attemptSvc.SubmitCode(progress, "any")
	require.NoError(t, err)
	assert.False(t, success)

	err = attemptSvc.AcceptPendingAttempt(progress)
	require.NoError(t, err)

	var last game.Attempt
	db.Where("level_progress_id = ?", progress.ID).Order("created_at DESC").First(&last)
	assert.True(t, last.Success)
}

// ---------- LevelProgressService ----------

func TestLevelProgressService_InitFirstLevel(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	progressSvc := game.NewLevelProgressService(db)

	author := createUser(t, db, "lp@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "LP Game")
	createLevel(t, db, g.ID, "L1", 1)

	var gameWithLevels game.Game
	db.Preload("Levels").First(&gameWithLevels, g.ID)

	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)

	err := progressSvc.InitFirstLevel(context.Background(), passing.ID)
	require.NoError(t, err)

	var progress game.LevelProgress
	db.Where("game_passing_id = ?", passing.ID).First(&progress)
	assert.Equal(t, gameWithLevels.Levels[0].ID, progress.LevelID)
}

func TestLevelProgressService_CompleteLevel(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	progressSvc := game.NewLevelProgressService(db)

	author := createUser(t, db, "complete@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Complete Game")
	_ = createLevel(t, db, g.ID, "L1", 1)
	l2 := createLevel(t, db, g.ID, "L2", 2)

	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)
	require.NoError(t, progressSvc.InitFirstLevel(context.Background(), passing.ID))

	var progress game.LevelProgress
	db.Where("game_passing_id = ? AND finished_at IS NULL", passing.ID).First(&progress)

	err := game.CompleteLevel(db, &progress)
	require.NoError(t, err)

	db.First(&progress, progress.ID)
	assert.NotNil(t, progress.FinishedAt)

	var next game.LevelProgress
	require.NoError(t, db.Where("game_passing_id = ? AND finished_at IS NULL", passing.ID).First(&next).Error)
	assert.Equal(t, l2.ID, next.LevelID)
}

func TestLevelProgressService_FinishGame(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	progressSvc := game.NewLevelProgressService(db)

	author := createUser(t, db, "finish@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Finish Game")
	_ = createLevel(t, db, g.ID, "L1", 1)

	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)
	require.NoError(t, progressSvc.InitFirstLevel(context.Background(), passing.ID))

	var progress game.LevelProgress
	db.Where("game_passing_id = ? AND finished_at IS NULL", passing.ID).First(&progress)

	err := game.CompleteLevel(db, &progress)
	require.NoError(t, err)

	var updated game.GamePassing
	db.First(&updated, passing.ID)
	assert.Equal(t, game.StatusFinished, updated.Status)
}

// ---------- CoAuthorService ----------

func TestCoAuthorService_AddAndRemove(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	coSvc := game.NewCoAuthorService(db)

	owner := createUser(t, db, "owner@test.com", "pass")
	coAuthor := createUser(t, db, "co@test.com", "pass")
	g := createPublishedGame(t, db, owner.ID, "Co Game")

	err := coSvc.Add(g.ID, coAuthor.ID, owner.ID)
	require.NoError(t, err)
	isManager, _ := coSvc.IsUserManager(g.ID, coAuthor.ID)
	assert.True(t, isManager)

	err = coSvc.Remove(g.ID, coAuthor.ID, owner.ID)
	require.NoError(t, err)
	isManager, _ = coSvc.IsUserManager(g.ID, coAuthor.ID)
	assert.False(t, isManager)
}

// ---------- Вспомогательные функции ----------

func newGameService(db *gorm.DB) *game.GameService {
	cfg := &config.Config{}
	hub := websocket.NewRoomHub()
	monitorSvc := game.NewMonitorService(db)
	gameRepo := game.NewGormGameRepo(db)
	passingRepo := game.NewGormGamePassingRepo(db)
	coAuthorSvc := game.NewCoAuthorService(db)
	attemptSvc := game.NewAttemptService(db)
	progressSvc := game.NewLevelProgressService(db)

	return game.NewGameService(
		gameRepo,
		passingRepo,
		coAuthorSvc,
		nil,
		monitorSvc,
		hub,
		attemptSvc,
		progressSvc,
		cfg,
	)
}

func newPassingService(db *gorm.DB) *game.GamePassingService {
	teamRepo := team.NewGormTeamRepo(db)
	authorizer := &testAuthorizer{}
	teamSvc := team.NewTeamService(teamRepo, authorizer)
	ca := game.NewCoAuthorService(db)
	return game.NewGamePassingService(db, teamSvc, ca)
}

type testAuthorizer struct{}

func (t *testAuthorizer) IsUserManager(gameID, userID uint) (bool, error) {
	return true, nil
}
func (t *testAuthorizer) HasPermission(gameID, userID uint, role string) (bool, error) {
	return true, nil
}

func createUser(t *testing.T, db *gorm.DB, email, _ string) *user.User {
	t.Helper()
	u := &user.User{Email: email, Password: "hashed", Name: email}
	require.NoError(t, db.Create(u).Error)
	return u
}

func createAdmin(t *testing.T, db *gorm.DB, email, _ string) *user.User {
	t.Helper()
	u := &user.User{Email: email, Password: "hashed", Name: email, Role: "admin"}
	require.NoError(t, db.Create(u).Error)
	return u
}

func createPublishedGame(t *testing.T, db *gorm.DB, authorID uint, name string) *game.Game {
	t.Helper()
	g := &game.Game{Name: name, AuthorID: authorID, IsDraft: false}
	require.NoError(t, db.Create(g).Error)
	db.Model(g).Update("is_draft", false)
	return g
}

func createTeam(t *testing.T, db *gorm.DB, captainID uint) *team.Team {
	t.Helper()
	tm := &team.Team{Name: "Test Team", CaptainID: captainID}
	require.NoError(t, db.Create(tm).Error)
	return tm
}

func createTeamWithCaptain(t *testing.T, db *gorm.DB, captainID uint) *team.Team {
	return createTeam(t, db, captainID)
}

func createPassing(t *testing.T, db *gorm.DB, gameID, teamID uint, status game.GamePassingStatus) *game.GamePassing {
	t.Helper()
	p := &game.GamePassing{GameID: gameID, TeamID: teamID, Status: status}
	require.NoError(t, db.Create(p).Error)
	return p
}

func createLevel(t *testing.T, db *gorm.DB, gameID uint, name string, position int) *level.Level {
	t.Helper()
	l := &level.Level{GameID: gameID, Name: name, Position: position}
	require.NoError(t, db.Create(l).Error)
	return l
}

func createLevelWithAnswer(t *testing.T, db *gorm.DB, gameID uint, name string, position int, code string) *level.Level {
	t.Helper()
	l := createLevel(t, db, gameID, name, position)
	q := &level.Question{LevelID: l.ID, Text: "Q"}
	require.NoError(t, db.Create(q).Error)
	a := &level.Answer{QuestionID: q.ID, Code: code}
	require.NoError(t, db.Create(a).Error)
	return l
}

func createBlackboxLevel(t *testing.T, db *gorm.DB, gameID uint, name string, position int) *level.Level {
	t.Helper()
	l := &level.Level{GameID: gameID, Name: name, Position: position, Type: level.TypeBlackbox}
	require.NoError(t, db.Create(l).Error)
	return l
}

func createLevelProgress(t *testing.T, db *gorm.DB, passingID, levelID uint, finished bool) *game.LevelProgress {
	t.Helper()
	p := &game.LevelProgress{
		GamePassingID: passingID,
		LevelID:       levelID,
		StartedAt:     time.Now(),
	}
	if finished {
		now := time.Now()
		p.FinishedAt = &now
	}
	require.NoError(t, db.Create(p).Error)
	return p
}
