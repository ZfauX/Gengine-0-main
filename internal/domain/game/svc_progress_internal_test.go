// internal/domain/game/svc_progress_internal_test.go
package game

import (
	"context"
	"testing"
	"time"

	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTimeoutDB(t *testing.T) *gorm.DB {
	t.Helper()
	return testutil.SetupPostgresDB(t,
		&Game{}, &GamePassing{}, &GameSetting{}, &LevelProgress{}, &LevelProgress{},
		&level.Level{}, &team.Team{}, &user.User{},
	)
}

// TestCheckTimeouts_MarksTimedOutAndAdvances: просроченный прогресс (по
// game_settings.per_level_time_limit) завершается и создаётся следующий (T-H2).
func TestCheckTimeouts_MarksTimedOutAndAdvances(t *testing.T) {
	db := setupTimeoutDB(t)
	ctx := context.Background()

	author := &user.User{Email: "t@test.com", Name: "T"}
	require.NoError(t, db.Create(author).Error)

	g := &Game{Name: "Timeout Game", AuthorID: author.ID}
	require.NoError(t, db.Create(g).Error)

	l1 := &level.Level{GameID: g.ID, Name: "L1", Position: 1}
	l2 := &level.Level{GameID: g.ID, Name: "L2", Position: 2}
	require.NoError(t, db.Create(l1).Error)
	require.NoError(t, db.Create(l2).Error)

	// Лимит 1 минута.
	require.NoError(t, db.Create(&GameSetting{GameID: g.ID, PerLevelTimeLimit: 1}).Error)

	tm := &team.Team{Name: "T1", CaptainID: author.ID}
	require.NoError(t, db.Create(tm).Error)
	passing := &GamePassing{GameID: g.ID, TeamID: tm.ID, Status: StatusStarted}
	require.NoError(t, db.Create(passing).Error)

	// Прогресс начат 2 минуты назад — просрочен.
	started := time.Now().Add(-2 * time.Minute)
	require.NoError(t, db.Create(&LevelProgress{
		GamePassingID: passing.ID,
		LevelID:       l1.ID,
		StartedAt:     started,
	}).Error)

	checkTimeoutsImpl(db, ctx, nil)

	var finished LevelProgress
	require.NoError(t, db.Where("game_passing_id = ? AND level_id = ?", passing.ID, l1.ID).First(&finished).Error)
	require.NotNil(t, finished.FinishedAt, "просроченный прогресс должен быть завершён")

	var next LevelProgress
	require.NoError(t, db.Where("game_passing_id = ? AND level_id = ?", passing.ID, l2.ID).First(&next).Error)
	assert.Nil(t, next.FinishedAt, "следующий уровень начат и активен")
}

// TestCheckTimeouts_NoLimitDoesNothing: без per_level_time_limit ничего не
// завершается (T-H2).
func TestCheckTimeouts_NoLimitDoesNothing(t *testing.T) {
	db := setupTimeoutDB(t)
	ctx := context.Background()

	author := &user.User{Email: "nl@test.com", Name: "NL"}
	require.NoError(t, db.Create(author).Error)
	g := &Game{Name: "No Limit", AuthorID: author.ID}
	require.NoError(t, db.Create(g).Error)

	l1 := &level.Level{GameID: g.ID, Name: "L1", Position: 1}
	require.NoError(t, db.Create(l1).Error)

	// Без game_settings → per_level_time_limit = 0 (COALESCE) → не таймаутит.
	tm := &team.Team{Name: "T1", CaptainID: author.ID}
	require.NoError(t, db.Create(tm).Error)
	passing := &GamePassing{GameID: g.ID, TeamID: tm.ID, Status: StatusStarted}
	require.NoError(t, db.Create(passing).Error)
	require.NoError(t, db.Create(&LevelProgress{
		GamePassingID: passing.ID,
		LevelID:       l1.ID,
		StartedAt:     time.Now().Add(-2 * time.Minute),
	}).Error)

	checkTimeoutsImpl(db, ctx, nil)

	var p LevelProgress
	require.NoError(t, db.Where("game_passing_id = ?", passing.ID).First(&p).Error)
	assert.Nil(t, p.FinishedAt, "без лимита прогресс не должен завершаться")
}

// TestCheckTimeouts_NotYetTimedOut: активный прогресс не трогается (T-H2).
func TestCheckTimeouts_NotYetTimedOut(t *testing.T) {
	db := setupTimeoutDB(t)
	ctx := context.Background()

	author := &user.User{Email: "nt@test.com", Name: "NT"}
	require.NoError(t, db.Create(author).Error)
	g := &Game{Name: "Not Timeout", AuthorID: author.ID}
	require.NoError(t, db.Create(g).Error)
	l1 := &level.Level{GameID: g.ID, Name: "L1", Position: 1}
	require.NoError(t, db.Create(l1).Error)
	require.NoError(t, db.Create(&GameSetting{GameID: g.ID, PerLevelTimeLimit: 10}).Error)

	tm := &team.Team{Name: "T1", CaptainID: author.ID}
	require.NoError(t, db.Create(tm).Error)
	passing := &GamePassing{GameID: g.ID, TeamID: tm.ID, Status: StatusStarted}
	require.NoError(t, db.Create(passing).Error)
	require.NoError(t, db.Create(&LevelProgress{
		GamePassingID: passing.ID,
		LevelID:       l1.ID,
		StartedAt:     time.Now().Add(-1 * time.Minute), // < 10 мин
	}).Error)

	checkTimeoutsImpl(db, ctx, nil)

	var p LevelProgress
	require.NoError(t, db.Where("game_passing_id = ?", passing.ID).First(&p).Error)
	assert.Nil(t, p.FinishedAt, "непросроченный прогресс не завершается")
}
