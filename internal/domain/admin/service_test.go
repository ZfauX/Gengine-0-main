// internal/domain/admin/service_test.go
package admin_test

import (
	"context"
	"testing"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/admin"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// mockBackupRepo — мок для admin.BackupRepository
type mockBackupRepo struct {
	mock.Mock
}

func (m *mockBackupRepo) Create(ctx context.Context, backup *admin.Backup) error {
	args := m.Called(ctx, backup)
	return args.Error(0)
}

func (m *mockBackupRepo) GetByID(ctx context.Context, id uint) (*admin.Backup, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*admin.Backup), args.Error(1)
}

func (m *mockBackupRepo) List(ctx context.Context) ([]admin.Backup, error) {
	args := m.Called(ctx)
	return args.Get(0).([]admin.Backup), args.Error(1)
}

func (m *mockBackupRepo) Delete(ctx context.Context, id uint) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *mockBackupRepo) Count(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}

func TestBackupService_CreateNow(t *testing.T) {
	// Этот тест требует наличия pg_dump в PATH, поэтому пропускаем.
	// В реальном CI лучше использовать интеграционные тесты с реальным PostgreSQL.
	t.Skip("требуется pg_dump, пропускаем в CI")
}

func TestBackupService_RotateBackups(t *testing.T) {
	mockRepo := new(mockBackupRepo)
	dbCfg := config.DatabaseConfig{
		Host:     "localhost",
		Port:     "5432",
		User:     "test",
		Password: "test",
		Name:     "testdb",
	}
	svc := admin.NewBackupService(mockRepo, "/tmp/backups", 2, dbCfg)

	ctx := context.Background()
	now := time.Now()

	// Подготовка данных: 3 бекапа
	backups := []admin.Backup{
		{ID: 1, FilePath: "/tmp/backups/backup1.sql", CreatedAt: now.Add(-3 * time.Hour)},
		{ID: 2, FilePath: "/tmp/backups/backup2.sql", CreatedAt: now.Add(-2 * time.Hour)},
		{ID: 3, FilePath: "/tmp/backups/backup3.sql", CreatedAt: now.Add(-1 * time.Hour)},
	}

	mockRepo.On("Count", ctx).Return(int64(3), nil)
	mockRepo.On("List", ctx).Return(backups, nil)

	// Ожидаем, что будут удалены два старых бекапа (ID 1 и 2)
	mockRepo.On("Delete", ctx, uint(1)).Return(nil)
	mockRepo.On("Delete", ctx, uint(2)).Return(nil)

	err := svc.RotateBackups(ctx)
	require.NoError(t, err)

	mockRepo.AssertExpectations(t)
}

func TestBackupService_GetMaxBackups(t *testing.T) {
	mockRepo := new(mockBackupRepo)
	dbCfg := config.DatabaseConfig{}
	svc := admin.NewBackupService(mockRepo, "/tmp", 5, dbCfg)
	assert.Equal(t, 5, svc.GetMaxBackups())

	// Проверка, что значение по умолчанию 10
	svc2 := admin.NewBackupService(mockRepo, "/tmp", 0, dbCfg)
	assert.Equal(t, 10, svc2.GetMaxBackups())
}
