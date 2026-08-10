// internal/domain/game/game_phase3_test.go
// Фаза 3 (Эпик C, pass 45): маршруты команд, индивидуальный старт,
// персональные ответы, коды на человека.
package game_test

import (
	"context"
	"testing"
	"time"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupPhase3DB(t *testing.T) *gorm.DB {
	t.Helper()
	return testutil.SetupPostgresDB(t, allModels...)
}

// C-1/C-2: маршрут команды.
func TestPhase3_TeamRoute(t *testing.T) {
	db := setupPhase3DB(t)
	ctx := context.Background()

	author := createUser(t, db, "route-author@test.com", "pass")
	g := createPhase3Game(t, db, author.ID, "Route Game")
	lvl1 := createPhase3Level(t, db, g.ID, 1)
	lvl2 := createPhase3Level(t, db, g.ID, 2)
	lvl3 := createPhase3Level(t, db, g.ID, 3)
	tm := createTeam(t, db, author.ID)
	passing := createPhase3Passing(t, db, g.ID, tm.ID)

	// Назначаем маршрут: 2 -> 3 -> 1 (нестандартный порядок).
	passingRepo := game.NewGormGamePassingRepo(db)
	require.NoError(t, passingRepo.SetTeamRoute(ctx, passing.ID, []uint{lvl2.ID, lvl3.ID, lvl1.ID}))

	route, err := passingRepo.GetTeamRoute(ctx, passing.ID)
	require.NoError(t, err)
	require.Len(t, route, 3)
	assert.Equal(t, lvl2.ID, route[0].LevelID)
	assert.Equal(t, lvl3.ID, route[1].LevelID)
	assert.Equal(t, lvl1.ID, route[2].LevelID)
}

// C-3: индивидуальное время старта.
func TestPhase3_TeamStartTime(t *testing.T) {
	db := setupPhase3DB(t)
	ctx := context.Background()

	author := createUser(t, db, "st-author@test.com", "pass")
	g := createPhase3Game(t, db, author.ID, "Start Game")
	tm := createTeam(t, db, author.ID)
	passing := createPhase3Passing(t, db, g.ID, tm.ID)

	start := time.Now().Add(2 * time.Hour).Truncate(time.Minute)
	passingRepo := game.NewGormGamePassingRepo(db)
	require.NoError(t, passingRepo.SetPassingStartTime(ctx, passing.ID, &start))

	loaded, err := passingRepo.GetByID(ctx, passing.ID)
	require.NoError(t, err)
	require.NotNil(t, loaded.StartTime)
	assert.WithinDuration(t, start, *loaded.StartTime, time.Minute)
}

// C-4: персональный ответ команды.
func TestPhase3_TeamAnswer(t *testing.T) {
	db := setupPhase3DB(t)
	ctx := context.Background()

	author := createUser(t, db, "ans-author@test.com", "pass")
	g := createPhase3Game(t, db, author.ID, "Answer Game")
	lvl := createPhase3Level(t, db, g.ID, 1)
	tmA := createTeam(t, db, author.ID)

	passingRepo := game.NewGormGamePassingRepo(db)
	require.NoError(t, passingRepo.SetTeamAnswer(ctx, lvl.ID, tmA.ID, "TEAM_A_SECRET", "подсказка А"))

	ans, err := passingRepo.GetTeamAnswer(ctx, lvl.ID, tmA.ID)
	require.NoError(t, err)
	assert.Equal(t, "TEAM_A_SECRET", ans.Code)
	assert.Equal(t, "подсказка А", ans.Hint)

	// У другой команды персонального ответа нет.
	_, err = passingRepo.GetTeamAnswer(ctx, lvl.ID, tmA.ID+1000)
	assert.Error(t, err)
}

// C-5: коды на человека.
func TestPhase3_AttemptsPerUser(t *testing.T) {
	db := setupPhase3DB(t)
	ctx := context.Background()

	author := createUser(t, db, "per-user@test.com", "pass")
	g := createPhase3Game(t, db, author.ID, "PerUser Game")
	lvl := createPhase3Level(t, db, g.ID, 1)
	tm := createTeam(t, db, author.ID)
	passing := createPhase3Passing(t, db, g.ID, tm.ID)

	u1 := createUser(t, db, "player1@test.com", "pass")
	u2 := createUser(t, db, "player2@test.com", "pass")

	progress := &game.LevelProgress{GamePassingID: passing.ID, LevelID: lvl.ID, StartedAt: time.Now()}
	require.NoError(t, db.Create(progress).Error)

	uid1 := u1.ID
	uid2 := u2.ID
	require.NoError(t, db.Create(&game.Attempt{LevelProgressID: progress.ID, UserID: &uid1, Code: "1111", Success: true}).Error)
	require.NoError(t, db.Create(&game.Attempt{LevelProgressID: progress.ID, UserID: &uid1, Code: "2222", Success: true}).Error)
	require.NoError(t, db.Create(&game.Attempt{LevelProgressID: progress.ID, UserID: &uid2, Code: "3333", Success: false}).Error)

	passingRepo := game.NewGormGamePassingRepo(db)
	rows, err := passingRepo.GetAttemptsPerUser(ctx, g.ID)
	require.NoError(t, err)
	// Успешные: только u1 (2 кода). u2 имеет 0 успешных.
	require.Len(t, rows, 1)
	assert.Equal(t, u1.ID, rows[0].UserID)
	assert.Equal(t, 2, rows[0].Count)
}

// ---------- helpers ----------

func createPhase3Game(t *testing.T, db *gorm.DB, authorID uint, name string) *game.Game {
	t.Helper()
	g := &game.Game{Name: name, AuthorID: authorID, Visibility: "public"}
	require.NoError(t, db.Create(g).Error)
	return g
}

func createPhase3Level(t *testing.T, db *gorm.DB, gameID uint, position int) *level.Level {
	t.Helper()
	l := &level.Level{GameID: gameID, Name: "Level", Position: position}
	require.NoError(t, db.Create(l).Error)
	return l
}

func createPhase3Passing(t *testing.T, db *gorm.DB, gameID, teamID uint) *game.GamePassing {
	t.Helper()
	p := &game.GamePassing{GameID: gameID, TeamID: teamID, Status: game.StatusAccepted}
	require.NoError(t, db.Create(p).Error)
	return p
}

// G-1..G-4 (pass 45): позиции игроков (upsert + чтение по игре).
func TestPhase35_PlayerLocations(t *testing.T) {
	db := setupPhase3DB(t)
	ctx := context.Background()

	author := createUser(t, db, "geo@test.com", "pass")
	g := createPhase3Game(t, db, author.ID, "Geo Game")
	tm := createTeam(t, db, author.ID)
	passing := createPhase3Passing(t, db, g.ID, tm.ID)
	u1 := createUser(t, db, "geo1@test.com", "pass")

	repo := game.NewGormGeolocationRepo(db)
	svc := game.NewGeolocationService(repo)

	// Запись позиции.
	require.NoError(t, svc.UpdateLocation(ctx, passing.ID, tm.ID, u1.ID, 55.7558, 37.6173, 10))

	// Обновление той же позиции (upsert — одна запись).
	require.NoError(t, svc.UpdateLocation(ctx, passing.ID, tm.ID, u1.ID, 55.7560, 37.6180, 8))

	locs, err := svc.LocationsByGame(ctx, g.ID)
	require.NoError(t, err)
	require.Len(t, locs, 1)
	assert.Equal(t, u1.ID, locs[0].UserID)
	assert.InDelta(t, 55.7560, locs[0].Latitude, 0.0001)
	assert.InDelta(t, 37.6180, locs[0].Longitude, 0.0001)
	assert.True(t, locs[0].IsFresh(time.Now()))
}
