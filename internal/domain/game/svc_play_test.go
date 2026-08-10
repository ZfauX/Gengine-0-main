// internal/domain/game/svc_play_test.go
// T-3 (pass 47): негативный путь SubmitCode — при сбое загрузки team_id
// запрос должен падать (транзакция откатывается), а не продолжаться с team_id=0.
package game

import (
	"context"
	"errors"
	"testing"
	"time"

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

// TestSubmitCode_TeamIDLoadFailure: сбой SELECT team_id из game_passings
// должен вернуть ошибку и НЕ вызывать SubmitCodeWithTx с team_id=0.
func TestSubmitCode_TeamIDLoadFailure(t *testing.T) {
	gormDB, mock := newMockPlayDB(t)

	// Транзакция: BEGIN → запросы → ROLLBACK (при ошибке).
	mock.ExpectBegin()

	// GetCurrentProgressForUpdate: SELECT ... FROM level_progresses WHERE game_passing_id=$1 AND finished_at IS NULL ... FOR UPDATE
	progressRows := sqlmock.NewRows([]string{"id", "game_passing_id", "level_id", "started_at"}).
		AddRow(1, 1, 1, time.Now())
	mock.ExpectQuery(`SELECT \* FROM "level_progresses" WHERE \(game_passing_id = \$1 AND finished_at IS NULL\).*`).
		WithArgs(1, 1).
		WillReturnRows(progressRows)

	// CheckTeamMembership: SELECT * FROM game_passings ... FOR UPDATE
	passingRows := sqlmock.NewRows([]string{"id", "game_id", "team_id", "status"}).
		AddRow(1, 10, 20, "started")
	mock.ExpectQuery(`SELECT \* FROM "game_passings" WHERE "game_passings"\."id" = \$1.*`).
		WithArgs(1, 1).
		WillReturnRows(passingRows)

	// CheckTeamMembership: COUNT team_members
	mock.ExpectQuery(`SELECT count\(\*\) FROM "team_members" WHERE team_id = \$1 AND user_id = \$2.*`).
		WithArgs(20, 7).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	// Сбой загрузки team_id (S-46-2): SELECT team_id FROM game_passings
	mock.ExpectQuery(`SELECT team_id FROM "game_passings" WHERE id = \$1.*`).
		WithArgs(1).
		WillReturnError(errors.New("connection reset"))

	// Транзакция откатывается (rollback запроса нет — GORM делает ROLLBACK).
	mock.ExpectRollback()

	attemptSvc := NewAttemptService()
	monitorSvc := NewMonitorService(gormDB)
	coAuthorSvc := NewCoAuthorService()
	hub := ws.NewRoomHub()
	playSvc := NewGamePlayService(gormDB, attemptSvc, monitorSvc, hub, coAuthorSvc)

	_, err := playSvc.SubmitCode(context.Background(), 1, 7, "secret")
	require.Error(t, err, "сбой загрузки team_id должен вернуть ошибку")
	require.Contains(t, err.Error(), "load passing team")

	require.NoError(t, mock.ExpectationsWereMet())
}
