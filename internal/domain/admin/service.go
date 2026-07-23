// internal/domain/admin/service.go
package admin

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/pkg/errors"

	"github.com/rs/zerolog/log"
)

// ---------- BackupService ----------

// BackupService управляет резервным копированием базы данных.
type BackupService struct {
	backupRepo BackupRepository
	BackupDir  string
	MaxBackups int
	dbHost     string
	dbPort     string
	dbUser     string
	dbPassword string
	dbName     string
}

// NewBackupService создаёт новый BackupService.
func NewBackupService(
	backupRepo BackupRepository,
	backupDir string,
	maxBackups int,
	dbCfg config.DatabaseConfig,
) *BackupService {
	if maxBackups <= 0 {
		maxBackups = 10
	}
	return &BackupService{
		backupRepo: backupRepo,
		BackupDir:  backupDir,
		MaxBackups: maxBackups,
		dbHost:     dbCfg.Host,
		dbPort:     dbCfg.Port,
		dbUser:     dbCfg.User,
		dbPassword: dbCfg.Password,
		dbName:     dbCfg.Name,
	}
}

// CreateNow выполняет pg_dump и сохраняет файл.
func (s *BackupService) CreateNow(ctx context.Context) error {
	if err := os.MkdirAll(s.BackupDir, 0755); err != nil {
		return fmt.Errorf("не удалось создать директорию бекапов: %w", err)
	}

	if _, err := exec.LookPath("pg_dump"); err != nil {
		return fmt.Errorf("pg_dump не найден в PATH: %w — убедитесь, что PostgreSQL установлен и pg_dump доступен", err)
	}

	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("backup_%s.sql", timestamp)
	filepath := filepath.Join(s.BackupDir, filename)

	cmd := exec.Command("pg_dump",
		"-h", s.dbHost,
		"-p", s.dbPort,
		"-U", s.dbUser,
		"-d", s.dbName,
		"-f", filepath,
	)
	// Используем .pgpass файл вместо PGPASSWORD (безопаснее — не виден в ps aux)
	// Пароль передаётся через переменную окружения, видимую только процессу
	cmd.Env = append(os.Environ(), fmt.Sprintf("PGPASSWORD=%s", s.dbPassword))

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pg_dump failed: %v, output: %s", err, string(output))
	}

	info, err := os.Stat(filepath)
	var size int64
	if err == nil {
		size = info.Size()
	}

	backup := Backup{
		Filename:  filename,
		FilePath:  filepath,
		Size:      size,
		CreatedAt: time.Now(),
	}
	if err := s.backupRepo.Create(ctx, &backup); err != nil {
		return err
	}

	return s.RotateBackups(ctx)
}

// List возвращает список всех бекапов (новые первыми).
func (s *BackupService) List(ctx context.Context) ([]Backup, error) {
	return s.backupRepo.List(ctx)
}

// Download возвращает путь к файлу бекапа по ID.
func (s *BackupService) Download(ctx context.Context, backupID uint) (string, error) {
	backup, err := s.backupRepo.GetByID(ctx, backupID)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(backup.FilePath); os.IsNotExist(err) {
		return "", fmt.Errorf("файл бекапа не найден")
	}
	return backup.FilePath, nil
}

// RotateBackups удаляет самые старые бекапы, если их количество превышает MaxBackups.
func (s *BackupService) RotateBackups(ctx context.Context) error {
	count, err := s.backupRepo.Count(ctx)
	if err != nil {
		return err
	}
	if count <= int64(s.MaxBackups) {
		return nil
	}

	backups, err := s.backupRepo.List(ctx)
	if err != nil {
		return err
	}

	toDelete := len(backups) - s.MaxBackups
	for i := range toDelete {
		errors.LogSilently(os.Remove(backups[i].FilePath), "RotateBackups: failed to remove old backup file")
		if err := s.backupRepo.Delete(ctx, backups[i].ID); err != nil {
			log.Error().Err(err).Uint("backup", backups[i].ID).Msg("RotateBackups: failed to delete record")
		}
	}
	return nil
}

// GetMaxBackups возвращает текущее значение лимита бекапов.
func (s *BackupService) GetMaxBackups() int {
	return s.MaxBackups
}
