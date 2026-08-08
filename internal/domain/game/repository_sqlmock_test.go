// internal/domain/game/repository_sqlmock_test.go
// A-M3 (pass 33): go-sqlmock-тесты для ключевых SQL-методов GameRepository —
// не требуют PostgreSQL и работают в `-short` режиме.
package game

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func newMockGorm(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	require.NoError(t, err)
	return gormDB, mock
}

func TestGormGameRepo_CountPassingsInStatuses(t *testing.T) {
	db, mock := newMockGorm(t)
	repo := NewGormGameRepo(db)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT count(*) FROM "game_passings" WHERE (game_id = $1 AND status IN ($2,$3)) AND "game_passings"."deleted_at" IS NULL`)).
		WithArgs(uint(7), StatusStarted, StatusTesting).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))

	count, err := repo.CountPassingsInStatuses(context.Background(), 7, []GamePassingStatus{StatusStarted, StatusTesting})
	require.NoError(t, err)
	require.Equal(t, int64(3), count)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGormGameRepo_CountPassingsInStatuses_Empty(t *testing.T) {
	db, mock := newMockGorm(t)
	repo := NewGormGameRepo(db)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT count(*) FROM "game_passings" WHERE (game_id = $1 AND status IN ($2,$3)) AND "game_passings"."deleted_at" IS NULL`)).
		WithArgs(uint(9), StatusStarted, StatusTesting).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	count, err := repo.CountPassingsInStatuses(context.Background(), 9, []GamePassingStatus{StatusStarted, StatusTesting})
	require.NoError(t, err)
	require.Equal(t, int64(0), count)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGormGameRepo_AdminListGames_Published(t *testing.T) {
	db, mock := newMockGorm(t)
	repo := NewGormGameRepo(db)

	mock.ExpectQuery(`SELECT games\.\*.*users\.name AS author__name.*COUNT\(\*\) OVER\(\) AS total_count.*WHERE 1=1.*is_draft = false.*ORDER BY games\.id DESC LIMIT \$1 OFFSET \$2`).
		WithArgs(10, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "created_at", "updated_at", "deleted_at", "author_id", "name",
			"description", "is_draft", "visibility", "starts_at", "registration_deadline",
			"max_team_number", "cover_path", "cover_url", "author__name", "total_count",
		}).
			AddRow(1, nil, nil, nil, 5, "Game A", "", false, "public", nil, nil, 10, "", "", "Author", 1))

	games, total, err := repo.AdminListGames(context.Background(), "", "published", 0, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, games, 1)
	require.Equal(t, "Game A", games[0].Name)
	require.NotNil(t, games[0].Author)
	require.Equal(t, "Author", games[0].Author.Name)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGormGameRepo_AdminListGames_WithQuery(t *testing.T) {
	db, mock := newMockGorm(t)
	repo := NewGormGameRepo(db)

	mock.ExpectQuery(`SELECT games\.\*.*users\.name AS author__name.*WHERE 1=1.*games\.name ILIKE \$1 OR users\.name ILIKE \$2.*ORDER BY games\.id DESC LIMIT \$3 OFFSET \$4`).
		WithArgs("%test%", "%test%", 5, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "created_at", "updated_at", "deleted_at", "author_id", "name",
			"description", "is_draft", "visibility", "starts_at", "registration_deadline",
			"max_team_number", "cover_path", "cover_url", "author__name", "total_count",
		}).
			AddRow(2, nil, nil, nil, 5, "Test Game", "", true, "public", nil, nil, 10, "", "", "Author", 0))

	// page=1, perPage=5 → offset=0
	games, total, err := repo.AdminListGames(context.Background(), "test", "", 0, 5)
	require.NoError(t, err)
	require.Equal(t, int64(0), total)
	require.Len(t, games, 1)
	require.Equal(t, "Test Game", games[0].Name)
	require.NoError(t, mock.ExpectationsWereMet())
}
