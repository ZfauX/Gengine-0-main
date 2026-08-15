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
	// L12 (PASS-17): сначала проверка существования (COUNT), при 0 — INSERT.
	mock.ExpectQuery(`SELECT count\(\*\) FROM "users" WHERE email = .+`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	// GORM Create оборачивает INSERT в транзакцию (skip_default_transaction=false).
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "users" .+`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectCommit()

	cfg := &config.Config{
		Admin: config.AdminConfig{Email: "admin@test.com", Password: "secret123"},
	}

	err := EnsureAdmin(db, cfg)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEnsureAdmin_AlreadyExists(t *testing.T) {
	db, mock := newMockDB(t)
	// L12 (PASS-17): если админ уже есть — НЕ хешируем пароль, не пишем в БД.
	mock.ExpectQuery(`SELECT count\(\*\) FROM "users" WHERE email = .+`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	cfg := &config.Config{
		Admin: config.AdminConfig{Email: "admin@test.com", Password: "secret123"},
	}

	err := EnsureAdmin(db, cfg)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEnsureAdmin_DBCreateError(t *testing.T) {
	db, mock := newMockDB(t)
	mock.ExpectQuery(`SELECT count\(\*\) FROM "users" WHERE email = .+`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "users" .+`).
		WillReturnError(assert.AnError)
	mock.ExpectRollback()

	cfg := &config.Config{
		Admin: config.AdminConfig{Email: "admin@test.com", Password: "secret123"},
	}

	err := EnsureAdmin(db, cfg)
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
