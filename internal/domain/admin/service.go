// internal/domain/admin/service.go
package admin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"gengine-0/internal/config"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// ---------- BackupService ----------

// BackupService управляет резервным копированием базы данных.
type BackupService struct {
	DB         *gorm.DB
	BackupDir  string
	MaxBackups int
	dbHost     string
	dbPort     string
	dbUser     string
	dbPassword string
	dbName     string
}

// NewBackupService создаёт новый BackupService.
func NewBackupService(db *gorm.DB, backupDir string, maxBackups int, dbCfg config.DatabaseConfig) *BackupService {
	if maxBackups <= 0 {
		maxBackups = 10
	}
	return &BackupService{
		DB:         db,
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
func (s *BackupService) CreateNow() error {
	// Создаём директорию, если её нет
	if err := os.MkdirAll(s.BackupDir, 0755); err != nil {
		return fmt.Errorf("не удалось создать директорию бекапов: %w", err)
	}

	// Проверяем, что pg_dump доступен
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
	cmd.Env = append(os.Environ(), fmt.Sprintf("PGPASSWORD=%s", s.dbPassword))

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pg_dump failed: %v, output: %s", err, string(output))
	}

	// Получаем размер файла
	info, err := os.Stat(filepath)
	var size int64
	if err == nil {
		size = info.Size()
	}

	// Сохраняем запись в БД
	backup := Backup{
		Filename:  filename,
		FilePath:  filepath,
		Size:      size,
		CreatedAt: time.Now(),
	}
	if err := s.DB.Create(&backup).Error; err != nil {
		return err
	}

	// Автоматическая ротация
	return s.RotateBackups()
}

// List возвращает список всех бекапов (новые первыми).
func (s *BackupService) List() ([]Backup, error) {
	var backups []Backup
	err := s.DB.Order("created_at DESC").Find(&backups).Error
	return backups, err
}

// Download возвращает путь к файлу бекапа по ID.
func (s *BackupService) Download(backupID uint) (string, error) {
	var backup Backup
	if err := s.DB.First(&backup, backupID).Error; err != nil {
		return "", err
	}
	if _, err := os.Stat(backup.FilePath); os.IsNotExist(err) {
		return "", fmt.Errorf("файл бекапа не найден")
	}
	return backup.FilePath, nil
}

// RotateBackups удаляет самые старые бекапы, если их количество превышает MaxBackups.
func (s *BackupService) RotateBackups() error {
	var count int64
	if err := s.DB.Model(&Backup{}).Count(&count).Error; err != nil {
		return err
	}
	if count <= int64(s.MaxBackups) {
		return nil
	}

	var backups []Backup
	if err := s.DB.Order("created_at ASC").Find(&backups).Error; err != nil {
		return err
	}

	toDelete := len(backups) - s.MaxBackups
	for i := range toDelete {
		_ = os.Remove(backups[i].FilePath)
		if err := s.DB.Delete(&backups[i]).Error; err != nil {
			log.Error().Err(err).Uint("backup", backups[i].ID).Msg("RotateBackups: failed to delete record")
		}
	}
	return nil
}

// GetMaxBackups возвращает текущее значение лимита бекапов.
func (s *BackupService) GetMaxBackups() int {
	return s.MaxBackups
}