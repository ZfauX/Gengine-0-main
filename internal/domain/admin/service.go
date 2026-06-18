// internal/domain/admin/service.go
package admin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

// ---------- AuditService ----------

// AuditService управляет журналом аудита.
type AuditService struct {
	DB *gorm.DB
}

// NewAuditService создаёт новый AuditService.
func NewAuditService(db *gorm.DB) *AuditService {
	return &AuditService{DB: db}
}

// Log записывает новое событие аудита.
func (s *AuditService) Log(userID uint, action, objectType string, objectID uint, details string) {
	entry := AuditLog{
		UserID:     userID,
		Action:     action,
		ObjectType: objectType,
		ObjectID:   objectID,
		Details:    details,
	}
	s.DB.Create(&entry)
}

// List возвращает записи аудита с пагинацией и фильтрацией.
func (s *AuditService) List(userIDStr, action string, page, perPage int) ([]AuditLog, int64, error) {
	query := s.DB.Model(&AuditLog{}).Preload("User")

	if userIDStr != "" {
		if id, err := strconv.Atoi(userIDStr); err == nil {
			query = query.Where("user_id = ?", id)
		}
	}
	if action != "" {
		query = query.Where("action = ?", action)
	}

	var total int64
	query.Count(&total)

	var logs []AuditLog
	offset := (page - 1) * perPage
	err := query.Order("created_at DESC").Offset(offset).Limit(perPage).Find(&logs).Error
	return logs, total, err
}

// ---------- BackupService ----------

// BackupService управляет резервным копированием базы данных.
type BackupService struct {
	DB         *gorm.DB
	BackupDir  string
	MaxBackups int
}

// NewBackupService создаёт новый BackupService.
func NewBackupService(db *gorm.DB, backupDir string, maxBackups int) *BackupService {
	if maxBackups <= 0 {
		maxBackups = 10
	}
	return &BackupService{
		DB:         db,
		BackupDir:  backupDir,
		MaxBackups: maxBackups,
	}
}

// CreateNow выполняет pg_dump и сохраняет файл.
func (s *BackupService) CreateNow() error {
	// Создаём директорию, если её нет
	if err := os.MkdirAll(s.BackupDir, 0755); err != nil {
		return fmt.Errorf("не удалось создать директорию бекапов: %w", err)
	}

	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("backup_%s.sql", timestamp)
	filepath := filepath.Join(s.BackupDir, filename)

	// Извлекаем параметры подключения из DATABASE_URL
	databaseURL := os.Getenv("DATABASE_URL")
	// Простейший парсинг: "postgres://user:pass@host:port/dbname?sslmode=disable"
	parts := strings.Split(databaseURL, "@")
	var userPass, hostPortDB string
	if len(parts) == 2 {
		userPass = strings.TrimPrefix(parts[0], "postgres://")
		hostPortDB = parts[1]
	}
	user := "postgres"
	password := ""
	if up := strings.Split(userPass, ":"); len(up) == 2 {
		user = up[0]
		password = up[1]
	}
	host := "localhost"
	port := "5432"
	dbname := "encounter"
	if hp := strings.Split(hostPortDB, "/"); len(hp) == 2 {
		hostPort := hp[0]
		dbname = strings.Split(hp[1], "?")[0]
		if h := strings.Split(hostPort, ":"); len(h) == 2 {
			host = h[0]
			port = h[1]
		} else {
			host = hostPort
		}
	}

	// Выполняем pg_dump
	cmd := exec.Command("pg_dump",
		"-h", host,
		"-p", port,
		"-U", user,
		"-d", dbname,
		"-f", filepath,
	)
	cmd.Env = append(os.Environ(), fmt.Sprintf("PGPASSWORD=%s", password))

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
	s.DB.Model(&Backup{}).Count(&count)
	if count <= int64(s.MaxBackups) {
		return nil
	}

	// Получаем список, сортируем по дате (старые — первые)
	var backups []Backup
	s.DB.Order("created_at ASC").Find(&backups)

	toDelete := len(backups) - s.MaxBackups
	for i := 0; i < toDelete; i++ {
		// Удаляем файл
		_ = os.Remove(backups[i].FilePath)
		// Удаляем запись из БД
		s.DB.Delete(&backups[i])
	}
	return nil
}

// GetMaxBackups возвращает текущее значение лимита бекапов.
func (s *BackupService) GetMaxBackups() int {
	return s.MaxBackups
}