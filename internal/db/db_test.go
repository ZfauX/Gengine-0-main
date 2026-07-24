package db

import (
	"os"
	"path/filepath"
	"testing"

	"gengine-0/internal/config"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func newMockDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	require.NoError(t, err)
	return gormDB, mock
}

func TestCreateMigrationFile(t *testing.T) {
	tmpDir := t.TempDir()
	upPath, downPath, err := CreateMigrationFile(tmpDir, "create_users")
	require.NoError(t, err)
	assert.Contains(t, upPath, "create_users.up.sql")
	assert.Contains(t, downPath, "create_users.down.sql")
	assert.FileExists(t, upPath)
	assert.FileExists(t, downPath)

	upContent, err := os.ReadFile(upPath)
	require.NoError(t, err)
	assert.Contains(t, string(upContent), "create_users up")

	downContent, err := os.ReadFile(downPath)
	require.NoError(t, err)
	assert.Contains(t, string(downContent), "create_users down")
}

func TestCreateMigrationFile_NestedDir(t *testing.T) {
	tmpDir := filepath.Join(t.TempDir(), "sub", "dir")
	upPath, downPath, err := CreateMigrationFile(tmpDir, "test")
	require.NoError(t, err)
	assert.FileExists(t, upPath)
	assert.FileExists(t, downPath)
}

func TestCreateMigrationFile_Timestamp(t *testing.T) {
	tmpDir := t.TempDir()
	upPath, _, err := CreateMigrationFile(tmpDir, "migration")
	require.NoError(t, err)

	base := filepath.Base(upPath)
	assert.Regexp(t, `^\d{14}_migration\.up\.sql$`, base)
}

func TestEnsureAdmin_CreatesNew(t *testing.T) {
	db, mock := newMockDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "users" .+ ON CONFLICT \("email"\) DO UPDATE SET "password"=.+`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectCommit()

	cfg := &config.Config{
		Admin: config.AdminConfig{Email: "admin@test.com", Password: "secret123"},
	}

	err := EnsureAdmin(db, cfg)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEnsureAdmin_DBCreateError(t *testing.T) {
	db, mock := newMockDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "users" .+ ON CONFLICT \("email"\) DO UPDATE SET "password"=.+`).
		WillReturnError(assert.AnError)
	mock.ExpectRollback()

	cfg := &config.Config{
		Admin: config.AdminConfig{Email: "admin@test.com", Password: "secret123"},
	}

	err := EnsureAdmin(db, cfg)
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEnsureAdmin_ConcurrentCreate(t *testing.T) {
	db, mock := newMockDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "users" .+ ON CONFLICT \("email"\) DO UPDATE SET "password"=.+`).
		WillReturnRows(sqlmock.NewRows([]string{"id"})) // no rows = conflict, RowsAffected = 0
	mock.ExpectCommit()

	cfg := &config.Config{
		Admin: config.AdminConfig{Email: "admin@test.com", Password: "secret123"},
	}

	err := EnsureAdmin(db, cfg)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
