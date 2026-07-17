// internal/domain/monitor/service_test.go
package monitor_test

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
	"gengine-0/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Все тесты используют единую тестовую базу PostgreSQL,
// которая автоматически мигрирует модели и очищает таблицы перед каждым тестом.

// ---------- MonitorService ----------

func TestMonitorService_GameSnapshot(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&monitor.ChatRoom{}, &monitor.ChatMessage{},
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{},
		&game.LevelProgress{}, &game.Attempt{},
		&monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&game.Log{},
		&level.Level{},
		&team.Team{},
		&user.User{},
	)
	ms := game.NewMonitorService(db)

	author := createUser(t, db, "auth@test.com", "pass")
	g := createGame(t, db, author.ID, "Snapshot Game")
	lvl := createLevel(t, db, g.ID, "L1", 1)

	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)
	createLevelProgress(t, db, passing.ID, lvl.ID, false)

	snapshot, err := ms.GameSnapshot(context.Background(), g.ID)
	require.NoError(t, err)
	assert.Len(t, snapshot, 1)
	assert.Equal(t, tm.Name, snapshot[0].TeamName)
	assert.Equal(t, 1, snapshot[0].TotalLevels)
	assert.False(t, snapshot[0].Finished)
}

func TestMonitorService_CalculateResults(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&monitor.ChatRoom{}, &monitor.ChatMessage{},
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{},
		&game.LevelProgress{}, &game.Attempt{},
		&monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&level.Level{},
		&team.Team{},
		&user.User{},
	)
	ms := game.NewMonitorService(db)

	author := createUser(t, db, "auth@test.com", "pass")
	g := createGame(t, db, author.ID, "Results Game")

	// Создаём уровень
	lvl := createLevel(t, db, g.ID, "Test Level", 1)

	tm1 := createTeam(t, db, author.ID)
	tm2 := createTeam(t, db, author.ID)

	p1 := createPassing(t, db, g.ID, tm1.ID, game.StatusFinished)
	p2 := createPassing(t, db, g.ID, tm2.ID, game.StatusFinished)

	// Создаём level_progresses с корректным finished_at
	baseTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d1 := 5 * time.Minute
	d2 := 10 * time.Minute

	createLevelProgress(t, db, p1.ID, lvl.ID, true)
	createLevelProgress(t, db, p2.ID, lvl.ID, true)

	// Устанавливаем durations через UPDATE
	// p1: started=baseTime, finished=baseTime+d1 => duration=5min
	// p2: started=baseTime, finished=baseTime+d2 => duration=10min
	db.Model(&game.LevelProgress{}).Where("game_passing_id = ?", p1.ID).Updates(map[string]interface{}{
		"started_at":  baseTime,
		"finished_at": baseTime.Add(d1),
	})
	db.Model(&game.LevelProgress{}).Where("game_passing_id = ?", p2.ID).Updates(map[string]interface{}{
		"started_at":  baseTime,
		"finished_at": baseTime.Add(d2),
	})

	err := ms.CalculateResults(context.Background(), g.ID)
	require.NoError(t, err)

	db.First(p1, p1.ID)
	db.First(p2, p2.ID)
	assert.NotNil(t, p1.Place)
	assert.NotNil(t, p2.Place)
	assert.Equal(t, 1, *p1.Place)
	assert.Equal(t, 2, *p2.Place)
}

func TestMonitorService_Cache(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&monitor.ChatRoom{}, &monitor.ChatMessage{},
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{},
		&game.LevelProgress{}, &game.Attempt{},
		&monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&level.Level{},
		&team.Team{},
		&user.User{},
	)
	ms := game.NewMonitorService(db)

	author := createUser(t, db, "auth@test.com", "pass")
	g := createGame(t, db, author.ID, "Cache Game")
	lvl := createLevel(t, db, g.ID, "L1", 1)

	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)
	createLevelProgress(t, db, passing.ID, lvl.ID, false)

	snap1, err := ms.GetOrFetchSnapshot(context.Background(), g.ID)
	assert.NoError(t, err)
	assert.NotNil(t, snap1)

	snap2, err := ms.GetOrFetchSnapshot(context.Background(), g.ID)
	assert.NoError(t, err)
	assert.NotNil(t, snap2)

	// Third call should use cache
	snap3, err := ms.GetOrFetchSnapshot(context.Background(), g.ID)
	require.NoError(t, err)
	assert.Len(t, snap3, 1)
}

// ---------- BlackboxVoteService ----------

func TestBlackboxVoteService_StartVoteAndClose(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&monitor.ChatRoom{}, &monitor.ChatMessage{},
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{},
		&game.LevelProgress{}, &game.Attempt{},
		&monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&level.Level{},
		&team.Team{},
		&user.User{},
	)
	cfg := &config.Config{}
	gameRepo := game.NewGormGameRepo(db)
	blackboxRepo := monitor.NewGormBlackboxRepo(db)
	voteSvc := monitor.NewBlackboxVoteService(blackboxRepo, gameRepo, cfg)

	author := createUser(t, db, "auth@test.com", "pass")
	g := createGame(t, db, author.ID, "Vote Game")
	lvl := createLevel(t, db, g.ID, "L1", 1)

	tm1 := createTeam(t, db, author.ID)
	tm2 := createTeam(t, db, author.ID)

	passing1 := createPassing(t, db, g.ID, tm1.ID, game.StatusStarted)
	_ = createPassing(t, db, g.ID, tm2.ID, game.StatusStarted)

	err := voteSvc.StartVoting(context.Background(), passing1.ID, lvl.ID, author.ID)
	require.NoError(t, err)

	progress := createLevelProgress(t, db, passing1.ID, lvl.ID, false)
	att := &game.Attempt{LevelProgressID: progress.ID, Code: "optA", Success: false}
	require.NoError(t, db.Create(att).Error)
	att2 := &game.Attempt{LevelProgressID: progress.ID, Code: "optB", Success: false}
	require.NoError(t, db.Create(att2).Error)

	err = voteSvc.Vote(context.Background(), 1, tm1.ID, "optA")
	require.NoError(t, err)
	err = voteSvc.Vote(context.Background(), 1, tm2.ID, "optB")
	require.NoError(t, err)

	results, err := voteSvc.GetVotingResults(context.Background(), 1)
	require.NoError(t, err)
	assert.Equal(t, 1, results["optA"])
	assert.Equal(t, 1, results["optB"])

	winner, err := voteSvc.CloseVoting(context.Background(), 1, author.ID)
	require.NoError(t, err)
	assert.Contains(t, []string{"optA", "optB"}, winner)
}

func TestBlackboxVoteService_DuplicateVote(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&monitor.ChatRoom{}, &monitor.ChatMessage{},
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{},
		&game.LevelProgress{}, &game.Attempt{},
		&monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&level.Level{},
		&team.Team{},
		&user.User{},
	)
	gameRepo := game.NewGormGameRepo(db)
	blackboxRepo := monitor.NewGormBlackboxRepo(db)
	voteSvc := monitor.NewBlackboxVoteService(blackboxRepo, gameRepo, &config.Config{})

	author := createUser(t, db, "auth@test.com", "pass")
	g := createGame(t, db, author.ID, "Dup Vote")
	lvl := createLevel(t, db, g.ID, "L1", 1)
	tm := createTeam(t, db, author.ID)
	passing := createPassing(t, db, g.ID, tm.ID, game.StatusStarted)

	require.NoError(t, voteSvc.StartVoting(context.Background(), passing.ID, lvl.ID, author.ID))

	prog := createLevelProgress(t, db, passing.ID, lvl.ID, false)
	db.Create(&game.Attempt{LevelProgressID: prog.ID, Code: "optA"})

	require.NoError(t, voteSvc.Vote(context.Background(), 1, tm.ID, "optA"))
	err := voteSvc.Vote(context.Background(), 1, tm.ID, "optA")
	assert.Error(t, err)
}

func TestBlackboxVoteService_CloseVotingNotAuthor(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&monitor.ChatRoom{}, &monitor.ChatMessage{},
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{},
		&game.LevelProgress{}, &game.Attempt{},
		&monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&level.Level{},
		&team.Team{},
		&user.User{},
	)
	gameRepo := game.NewGormGameRepo(db)
	blackboxRepo := monitor.NewGormBlackboxRepo(db)
	voteSvc := monitor.NewBlackboxVoteService(blackboxRepo, gameRepo, &config.Config{})

	author := createUser(t, db, "auth@test.com", "pass")
	other := createUser(t, db, "other@test.com", "pass")
	g := createGame(t, db, author.ID, "Not Author Close")
	lvl := createLevel(t, db, g.ID, "L1", 1)
	passing := createPassing(t, db, g.ID, createTeam(t, db, author.ID).ID, game.StatusStarted)

	require.NoError(t, voteSvc.StartVoting(context.Background(), passing.ID, lvl.ID, author.ID))

	_, err := voteSvc.CloseVoting(context.Background(), 1, other.ID)
	assert.Error(t, err)
}

// ---------- ChatService ----------

func TestChatService_CreateGameRoom(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&monitor.ChatRoom{}, &monitor.ChatMessage{},
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{},
		&game.LevelProgress{}, &game.Attempt{},
		&monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&level.Level{},
		&team.Team{},
		&user.User{},
	)
	chatRepo := monitor.NewGormChatRepo(db)
	cs := monitor.NewChatService(chatRepo)

	author := createUser(t, db, "chat@test.com", "pass")
	g := createGame(t, db, author.ID, "Chat Game")

	room, err := cs.GetOrCreateGameRoom(context.Background(), g.ID)
	require.NoError(t, err)
	assert.Equal(t, "Общий чат игры", room.Name)
	assert.Nil(t, room.TeamID)
}

func TestChatService_CreateTeamRoom(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&monitor.ChatRoom{}, &monitor.ChatMessage{},
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{},
		&game.LevelProgress{}, &game.Attempt{},
		&monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&level.Level{},
		&team.Team{},
		&user.User{},
	)
	chatRepo := monitor.NewGormChatRepo(db)
	cs := monitor.NewChatService(chatRepo)

	author := createUser(t, db, "teamchat@test.com", "pass")
	g := createGame(t, db, author.ID, "Team Chat Game")
	tm := createTeam(t, db, author.ID)

	room, err := cs.GetOrCreateTeamRoom(context.Background(), g.ID, tm.ID, 100)
	require.NoError(t, err)
	assert.Equal(t, "Командный чат", room.Name)
	assert.NotNil(t, room.TeamID)
}

func TestChatService_SaveAndGetMessages(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&monitor.ChatRoom{}, &monitor.ChatMessage{},
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{},
		&game.LevelProgress{}, &game.Attempt{},
		&monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&level.Level{},
		&team.Team{},
		&user.User{},
	)
	chatRepo := monitor.NewGormChatRepo(db)
	cs := monitor.NewChatService(chatRepo)

	author := createUser(t, db, "msg@test.com", "pass")
	g := createGame(t, db, author.ID, "Msg Game")

	room, err := cs.GetOrCreateGameRoom(context.Background(), g.ID)
	require.NoError(t, err)

	msg, err := cs.SaveMessage(context.Background(), room.ID, author.ID, "Hello")
	require.NoError(t, err)
	assert.Equal(t, "Hello", msg.Content)

	msgs, err := cs.GetMessages(context.Background(), room.ID, 10)
	require.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, "Hello", msgs[0].Content)
}

// ---------- Вспомогательные функции ----------

func createUser(t *testing.T, db *gorm.DB, email, _ string) *user.User {
	t.Helper()
	u := &user.User{Email: email, Password: "hashed", Name: email}
	require.NoError(t, db.Create(u).Error)
	return u
}

func createGame(t *testing.T, db *gorm.DB, authorID uint, name string) *game.Game {
	t.Helper()
	g := &game.Game{Name: name, AuthorID: authorID, IsDraft: false}
	require.NoError(t, db.Create(g).Error)
	db.Model(g).Update("is_draft", false)
	return g
}

func createTeam(t *testing.T, db *gorm.DB, captainID uint) *team.Team {
	t.Helper()
	tm := &team.Team{Name: "Team", CaptainID: captainID}
	require.NoError(t, db.Create(tm).Error)
	return tm
}

func createLevel(t *testing.T, db *gorm.DB, gameID uint, name string, position int) *level.Level {
	t.Helper()
	l := &level.Level{GameID: gameID, Name: name, Position: position}
	require.NoError(t, db.Create(l).Error)
	return l
}

func createPassing(t *testing.T, db *gorm.DB, gameID, teamID uint, status game.GamePassingStatus) *game.GamePassing {
	t.Helper()
	p := &game.GamePassing{GameID: gameID, TeamID: teamID, Status: status}
	require.NoError(t, db.Create(p).Error)
	return p
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
