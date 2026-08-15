// internal/domain/game/svc_play_test.go
// T-3 (pass 47) / DEEP-REVIEW LOW #28 (pass 46): teamID берётся из
// CheckTeamMembership (одно чтение passing с FOR UPDATE) — отдельного
// SELECT team_id больше нет, поэтому сбой на этом шаге невозможен.
package game

import (
	"context"
	"errors"
	"testing"
	"time"

	"gengine-0/internal/pkg/cache"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func newMockPlayDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	require.NoError(t, err)
	return gormDB, mock
}

// TestSubmitCode_TeamIDFromMembership: teamID приходит из CheckTeamMembership,
// и сбой на следующем шаге (загрузка уровня) НЕ должен быть "load passing team".
func TestSubmitCode_TeamIDFromMembership(t *testing.T) {
	gormDB, mock := newMockPlayDB(t)

	mock.ExpectBegin()

	// GetCurrentProgressForUpdate.
	progressRows := sqlmock.NewRows([]string{"id", "game_passing_id", "level_id", "started_at"}).
		AddRow(1, 1, 1, time.Now())
	mock.ExpectQuery(`SELECT \* FROM "level_progresses" WHERE \(game_passing_id = \$1 AND finished_at IS NULL\).*`).
		WithArgs(1, 1).
		WillReturnRows(progressRows)

	// CheckTeamMembership: passing FOR UPDATE + COUNT team_members.
	passingRows := sqlmock.NewRows([]string{"id", "game_id", "team_id", "status"}).
		AddRow(1, 10, 20, "started")
	mock.ExpectQuery(`SELECT \* FROM "game_passings" WHERE "game_passings"\."id" = \$1.*`).
		WithArgs(1, 1).
		WillReturnRows(passingRows)
	mock.ExpectQuery(`SELECT count\(\*\) FROM "team_members" WHERE team_id = \$1 AND user_id = \$2.*`).
		WithArgs(20, 7).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	// SubmitCodeWithTx: загрузка уровня — сбой здесь означает, что teamID уже
	// получен нормально (не "load passing team").
	mock.ExpectQuery(`SELECT \* FROM "levels" WHERE`).
		WillReturnError(errors.New("level load failed"))

	mock.ExpectRollback()

	attemptSvc := NewAttemptService(&cache.NoopCache{})
	monitorSvc := NewMonitorService(gormDB)
	coAuthorSvc := NewCoAuthorService()
	hub := ws.NewRoomHub()
	playSvc := NewGamePlayService(gormDB, attemptSvc, monitorSvc, hub, coAuthorSvc)

	_, err := playSvc.SubmitCode(context.Background(), 1, 7, "secret")
	require.Error(t, err)
	require.Contains(t, err.Error(), "level load failed", "ошибка должна прийти с загрузки уровня, а не team_id")

	require.NoError(t, mock.ExpectationsWereMet())
}
