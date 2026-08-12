// internal/domain/admin/service.go
package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/pkg/errors"

	"github.com/rs/zerolog/log"
)

// randomHex (DEEP-REVIEW PASS-4 H5): крипто-нонс для имён файлов бекапов.
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

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

	// backupMu (DEEP-REVIEW PASS-4 H5): сериализует CreateNow — повторный клик
	// во время дампа не запускает конкурирующий pg_dump.
	backupMu sync.Mutex

	// backupWG (DEEP-REVIEW PASS-6 L10): фоновые дампы отслеживаются — при
	// shutdown сервер ждёт завершения активного pg_dump (иначе процесс убивает
	// дамп посреди записи).
	backupWG sync.WaitGroup
}

// WaitForBackups (L10): ожидает завершения активных фоновых дампов
// (вызывается при graceful shutdown).
func (s *BackupService) WaitForBackups() {
	s.backupWG.Wait()
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

// CreateNowAsync запускает CreateNow в фоновой горутине (DEEP-REVIEW PASS-4 H5):
// HTTP-ответ не ждёт pg_dump до 10 мин; повторный вызов во время дампа
// не стартует конкурирующий процесс (backupMu). Ошибка логируется.
func (s *BackupService) CreateNowAsync(reqCtx context.Context) error {
	// Non-blocking: если дамп уже идёт — отвечаем «уже запущен».
	if !s.backupMu.TryLock() {
		return fmt.Errorf("создание бекапа уже выполняется")
	}
	s.backupWG.Add(1) // L10 (PASS-6): отслеживаем фоновый дамп
	go func() {
		// L5 (PASS-5): recover — паника в фоновой горутине не должна ронять процесс.
		defer func() {
			if r := recover(); r != nil {
				log.Error().Interface("panic", r).Msg("CreateBackup (async): panic recovered")
			}
		}()
		defer s.backupMu.Unlock()
		defer s.backupWG.Done() // L10 (PASS-6)
		// Независимый контекст — дисконнект админа не прерывает дамп.
		if err := s.CreateNow(context.WithoutCancel(reqCtx)); err != nil {
			log.Error().Err(err).Msg("CreateBackup (async): failed")
		}
	}()
	return nil
}

// CreateNow выполняет pg_dump и сохраняет файл.
func (s *BackupService) CreateNow(ctx context.Context) error {
	if err := os.MkdirAll(s.BackupDir, 0755); err != nil {
		return fmt.Errorf("не удалось создать директорию бекапов: %w", err)
	}

	if _, err := exec.LookPath("pg_dump"); err != nil {
		return fmt.Errorf("pg_dump не найден в PATH: %w — убедитесь, что PostgreSQL установлен и pg_dump доступен", err)
	}

	// DEEP-REVIEW PASS-4 H5: ms-таймстамп недостаточно — два клика в одну ms
	// перезаписывали файл. Добавлен крипто-нонс (4 байта hex) как в storage.
	timestamp := time.Now().Format("20060102_150405.000")
	nonce := randomHex(4)
	filename := fmt.Sprintf("backup_%s_%s.sql", timestamp, nonce)
	filepath := filepath.Join(s.BackupDir, filename)

	// DEEP-REVIEW PASS-2 (#8): собственный таймаут и НЕЗАВИСИМЫЙ контекст —
	// раньше dumpCtx = WithTimeout(ctx, ...) наследовал клиентский ctx, и
	// disconnect прерывал pg_dump посреди записи. Background гарантирует, что
	// дамп завершится, даже если админ закрыл вкладку.
	dumpCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(dumpCtx, "pg_dump",
		"-h", s.dbHost,
		"-p", s.dbPort,
		"-U", s.dbUser,
		"-d", s.dbName,
		"-f", filepath,
	)
	// Пароль передаётся через переменную окружения (без хардкода PGHOST/PGPORT — используем флаги -h/-p)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+s.dbPassword)

	output, err := cmd.CombinedOutput()
	if err != nil {
		// Чистим частичный файл (timeout/abort). L7 (PASS-3): логируем ошибку
		// удаления — иначе сироты на диске не видны.
		if rmErr := os.Remove(filepath); rmErr != nil {
			log.Warn().Err(rmErr).Str("file", filepath).Msg("Backup: failed to remove partial dump on pg_dump error")
		}
		return fmt.Errorf("pg_dump failed: %w, output: %s", err, string(output))
	}

	// L-7 (pass 40): дамп содержит хеши паролей, 2FA-секреты, refresh-хеши —
	// ограничиваем доступ только владельцем (was 0644 по умолчанию).
	if chmodErr := os.Chmod(filepath, 0600); chmodErr != nil {
		log.Warn().Err(chmodErr).Str("file", filepath).Msg("Backup: failed to chmod backup file to 0600")
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
// SEC1: путь к файлу проверяется — он обязан лежать внутри BackupDir.
// Defense-in-depth: даже при компрометации записи в БД бекап-сервис не
// отдаст произвольный файл файловой системы.
func (s *BackupService) Download(ctx context.Context, backupID uint) (string, error) {
	backup, err := s.backupRepo.GetByID(ctx, backupID)
	if err != nil {
		return "", err
	}

	absBackupDir, err := filepath.Abs(s.BackupDir)
	if err != nil {
		return "", fmt.Errorf("некорректная директория бекапов: %w", err)
	}
	absPath, err := filepath.Abs(backup.FilePath)
	if err != nil {
		return "", fmt.Errorf("некорректный путь бекапа: %w", err)
	}
	rel, err := filepath.Rel(absBackupDir, absPath)
	if err != nil || rel == ".." || len(rel) >= 3 && rel[:3] == ".."+string(os.PathSeparator) {
		return "", fmt.Errorf("путь бекапа выходит за пределы директории бекапов")
	}

	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return "", fmt.Errorf("файл бекапа не найден")
	}
	return absPath, nil
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

	// S-43-2 (pass 43): удаляем САМЫЕ СТАРЫЕ записи — List отдаёт новые
	// (DESC+LIMIT 100) и не подходит для ротации при count > 100.
	toDelete := int(count) - s.MaxBackups
	backups, err := s.backupRepo.ListOldest(ctx, toDelete)
	if err != nil {
		return err
	}

	for i := range backups {
		// H-1 (pass 41): удаляем только файлы внутри BackupDir — раньше
		// os.Remove по пути из БД без boundary-проверки (компрометация
		// записи = удаление произвольного файла ФС).
		if !s.isWithinBackupDir(backups[i].FilePath) {
			log.Warn().Str("file", backups[i].FilePath).Msg("RotateBackups: skipping file outside backup dir")
			continue
		}
		errors.LogSilently(os.Remove(backups[i].FilePath), "RotateBackups: failed to remove old backup file")
		if err := s.backupRepo.Delete(ctx, backups[i].ID); err != nil {
			log.Error().Err(err).Uint("backup", backups[i].ID).Msg("RotateBackups: failed to delete record")
		}
	}
	return nil
}

// isWithinBackupDir проверяет, что путь находится внутри BackupDir (boundary
// для Download/RotateBackups — защита от произвольного удаления/чтения).
func (s *BackupService) isWithinBackupDir(path string) bool {
	if path == "" {
		return false
	}
	absDir, err := filepath.Abs(s.BackupDir)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absDir, absPath)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// GetMaxBackups возвращает текущее значение лимита бекапов.
func (s *BackupService) GetMaxBackups() int {
	return s.MaxBackups
}
