// internal/domain/game/service_test.go
package game_test

import (
	"testing"
	"time"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/monitor"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Все тесты используют единую тестовую базу PostgreSQL,
// которая автоматически мигрирует модели и очищает таблицы перед каждым тестом.

// ---------- GameService ----------

func TestGameService_Create(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{}, &game.CoAuthor{}, &game.Note{},
		&game.LevelProgress{}, &game.Attempt{},
		&monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&game.Log{},
		&game.PlayerRating{},
		&level.Level{}, &level.Question{}, &level.Answer{},
		&team.Team{}, &team.Invitation{},
		&user.User{}, &user.Achievement{},
	)
	svc := newGameService(db)

	author := createUser(t, db, "author@test.com", "pass")
	g := &game.Game{Name: "Test Game"}

	err := svc.Create(g, author.ID)
	require.NoError(t, err)
	assert.True(t, g.IsDraft)
	assert.Equal(t, author.ID, g.AuthorID)
}

func TestGameService_Publish(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{}, &game.CoAuthor{}, &game.Note{},
		&game.LevelProgress{}, &game.Attempt{},
		&monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&game.PlayerRating{},
		&level.Level{}, &level.Question{}, &level.Answer{},
		&team.Team{}, &team.Invitation{},
		&user.User{}, &user.Achievement{},
	)
	svc := newGameService(db)

	author := createUser(t, db, "pub@test.com", "pass")
	g := &game.Game{Name: "Game", AuthorID: author.ID, IsDraft: true}
	require.NoError(t, db.Create(g).Error)

	err := svc.Publish(g.ID, author.ID)
	require.NoError(t, err)

	var updated game.Game
	db.First(&updated, g.ID)
	assert.False(t, updated.IsDraft)
}

func TestGameService_ForceFinishGame(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{}, &game.CoAuthor{}, &game.Note{},
		&game.LevelProgress{}, &game.Attempt{},
		&monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&game.PlayerRating{},
		&level.Level{}, &level.Question{}, &level.Answer{},
		&team.Team{}, &team.Invitation{},
		&user.User{}, &user.Achievement{},
	)
	svc := newGameService(db)

	author := createUser(t, db, "finish@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Finish Game")

	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)

	lvl := createLevel(t, db, g.ID, "L1", 1)
	createLevelProgress(t, db, passing.ID, lvl.ID, false)

	err := svc.ForceFinishGame(g.ID)
	require.NoError(t, err)

	var updated game.GamePassing
	db.First(&updated, passing.ID)
	assert.Equal(t, game.StatusFinished, updated.Status)
}

func TestGameService_DisqualifyTeam(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{}, &game.CoAuthor{}, &game.Note{},
		&game.LevelProgress{}, &game.Attempt{},
		&monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&game.PlayerRating{},
		&level.Level{}, &level.Question{}, &level.Answer{},
		&team.Team{}, &team.Invitation{},
		&user.User{}, &user.Achievement{},
	)
	svc := newGameService(db)

	author := createUser(t, db, "disq@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Disq Game")

	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)

	lvl := createLevel(t, db, g.ID, "L1", 1)
	createLevelProgress(t, db, passing.ID, lvl.ID, false)

	err := svc.DisqualifyTeam(g.ID, tm.ID)
	require.NoError(t, err)

	var updated game.GamePassing
	db.First(&updated, passing.ID)
	assert.Equal(t, game.StatusDisqualified, updated.Status)
}

// ---------- GamePassingService ----------

func TestGamePassingService_Apply(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{}, &game.CoAuthor{}, &game.Note{},
		&game.LevelProgress{}, &game.Attempt{},
		&monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&game.PlayerRating{},
		&level.Level{}, &level.Question{}, &level.Answer{},
		&team.Team{}, &team.Invitation{},
		&user.User{}, &user.Achievement{},
	)
	svc := newPassingService(db)

	author := createUser(t, db, "auth@test.com", "pass")
	cap := createUser(t, db, "cap@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Apply Game")
	tm := createTeamWithCaptain(t, db, cap.ID)

	err := svc.Apply(g.ID, tm.ID, cap.ID)
	require.NoError(t, err)

	var passing game.GamePassing
	db.Where("game_id = ? AND team_id = ?", g.ID, tm.ID).First(&passing)
	assert.Equal(t, game.StatusPending, passing.Status)
}

func TestGamePassingService_Apply_NotCaptain(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{}, &game.CoAuthor{}, &game.Note{},
		&game.LevelProgress{}, &game.Attempt{},
		&monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&game.PlayerRating{},
		&level.Level{}, &level.Question{}, &level.Answer{},
		&team.Team{}, &team.Invitation{},
		&user.User{}, &user.Achievement{},
	)
	svc := newPassingService(db)

	author := createUser(t, db, "auth2@test.com", "pass")
	other := createUser(t, db, "other@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Game")
	tm := createTeamWithCaptain(t, db, other.ID)

	err := svc.Apply(g.ID, tm.ID, other.ID+1)
	assert.Error(t, err)
}

func TestGamePassingService_Accept(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{}, &game.CoAuthor{}, &game.Note{},
		&game.LevelProgress{}, &game.Attempt{},
		&monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&game.PlayerRating{},
		&level.Level{}, &level.Question{}, &level.Answer{},
		&team.Team{}, &team.Invitation{},
		&user.User{}, &user.Achievement{},
	)
	svc := newPassingService(db)

	author := createUser(t, db, "author3@test.com", "pass")
	cap := createUser(t, db, "cap3@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Accept Game")
	tm := createTeamWithCaptain(t, db, cap.ID)

	require.NoError(t, svc.Apply(g.ID, tm.ID, cap.ID))

	var passing game.GamePassing
	require.NoError(t, db.Where("game_id = ? AND team_id = ?", g.ID, tm.ID).First(&passing).Error)

	err := svc.UpdateStatus(passing.ID, game.StatusAccepted, author.ID)
	require.NoError(t, err)

	db.First(&passing, passing.ID)
	assert.Equal(t, game.StatusAccepted, passing.Status)
}

func TestGamePassingService_StartGame(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{}, &game.CoAuthor{}, &game.Note{},
		&game.LevelProgress{}, &game.Attempt{},
		&monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&game.PlayerRating{},
		&level.Level{}, &level.Question{}, &level.Answer{},
		&team.Team{}, &team.Invitation{},
		&user.User{}, &user.Achievement{},
	)
	svc := newPassingService(db)

	author := createUser(t, db, "author4@test.com", "pass")
	cap := createUser(t, db, "cap4@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Start Game")
	tm := createTeamWithCaptain(t, db, cap.ID)

	require.NoError(t, svc.Apply(g.ID, tm.ID, cap.ID))
	var passing game.GamePassing
	require.NoError(t, db.Where("game_id = ? AND team_id = ?", g.ID, tm.ID).First(&passing).Error)
	require.NoError(t, svc.UpdateStatus(passing.ID, game.StatusAccepted, author.ID))

	err := svc.StartGame(passing.ID, cap.ID)
	require.NoError(t, err)

	db.First(&passing, passing.ID)
	assert.Equal(t, game.StatusStarted, passing.Status)
}

// ---------- AttemptService ----------

func TestAttemptService_SubmitCode_Correct(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{}, &game.CoAuthor{}, &game.Note{},
		&game.LevelProgress{}, &game.Attempt{},
		&monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&game.PlayerRating{},
		&level.Level{}, &level.Question{}, &level.Answer{},
		&team.Team{}, &team.Invitation{},
		&user.User{}, &user.Achievement{},
	)
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
	db := testutil.SetupPostgresDB(t,
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{}, &game.CoAuthor{}, &game.Note{},
		&game.LevelProgress{}, &game.Attempt{},
		&monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&game.PlayerRating{},
		&level.Level{}, &level.Question{}, &level.Answer{},
		&team.Team{}, &team.Invitation{},
		&user.User{}, &user.Achievement{},
	)
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
	db := testutil.SetupPostgresDB(t,
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{}, &game.CoAuthor{}, &game.Note{},
		&game.LevelProgress{}, &game.Attempt{},
		&monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&game.PlayerRating{},
		&level.Level{}, &level.Question{}, &level.Answer{},
		&team.Team{}, &team.Invitation{},
		&user.User{}, &user.Achievement{},
	)
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
	db := testutil.SetupPostgresDB(t,
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{}, &game.CoAuthor{}, &game.Note{},
		&game.LevelProgress{}, &game.Attempt{},
		&monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&game.PlayerRating{},
		&level.Level{}, &level.Question{}, &level.Answer{},
		&team.Team{}, &team.Invitation{},
		&user.User{}, &user.Achievement{},
	)
	progressSvc := game.NewLevelProgressService(db)

	author := createUser(t, db, "lp@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "LP Game")
	createLevel(t, db, g.ID, "L1", 1)

	var gameWithLevels game.Game
	db.Preload("Levels").First(&gameWithLevels, g.ID)

	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)

	err := progressSvc.InitFirstLevel(passing.ID)
	require.NoError(t, err)

	var progress game.LevelProgress
	db.Where("game_passing_id = ?", passing.ID).First(&progress)
	assert.Equal(t, gameWithLevels.Levels[0].ID, progress.LevelID)
}

func TestLevelProgressService_CompleteLevel(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{}, &game.CoAuthor{}, &game.Note{},
		&game.LevelProgress{}, &game.Attempt{},
		&monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&game.PlayerRating{},
		&level.Level{}, &level.Question{}, &level.Answer{},
		&team.Team{}, &team.Invitation{},
		&user.User{}, &user.Achievement{},
	)
	progressSvc := game.NewLevelProgressService(db)

	author := createUser(t, db, "complete@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Complete Game")
	_ = createLevel(t, db, g.ID, "L1", 1)
	l2 := createLevel(t, db, g.ID, "L2", 2)

	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)
	require.NoError(t, progressSvc.InitFirstLevel(passing.ID))

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
	db := testutil.SetupPostgresDB(t,
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{}, &game.CoAuthor{}, &game.Note{},
		&game.LevelProgress{}, &game.Attempt{},
		&monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&game.PlayerRating{},
		&level.Level{}, &level.Question{}, &level.Answer{},
		&team.Team{}, &team.Invitation{},
		&user.User{}, &user.Achievement{},
	)
	progressSvc := game.NewLevelProgressService(db)

	author := createUser(t, db, "finish@test.com", "pass")
	g := createPublishedGame(t, db, author.ID, "Finish Game")
	_ = createLevel(t, db, g.ID, "L1", 1)

	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)
	require.NoError(t, progressSvc.InitFirstLevel(passing.ID))

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
	db := testutil.SetupPostgresDB(t,
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{}, &game.CoAuthor{}, &game.Note{},
		&game.LevelProgress{}, &game.Attempt{},
		&monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&game.PlayerRating{},
		&level.Level{}, &level.Question{}, &level.Answer{},
		&team.Team{}, &team.Invitation{},
		&user.User{}, &user.Achievement{},
	)
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
	monitorSvc := game.NewMonitorService(db)
	return game.NewGameService(db,
		game.NewCoAuthorService(db),
		nil,
		monitorSvc,
		nil,
		game.NewAttemptService(db),
		game.NewLevelProgressService(db),
		nil,
	)
}

func newPassingService(db *gorm.DB) *game.GamePassingService {
	ts := team.NewTeamService(db)
	ca := game.NewCoAuthorService(db)
	return game.NewGamePassingService(db, ts, ca)
}

func createUser(t *testing.T, db *gorm.DB, email, password string) *user.User {
	t.Helper()
	u := &user.User{Email: email, Password: "hashed", Name: email}
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