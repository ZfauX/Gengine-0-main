// internal/domain/admin/service.go
package admin

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
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

	// encryptionKey (admin #6, PASS-8): AES-256 ключ шифрования бэкапов
	// (32 байта). Пустой — бэкапы не шифруются (защита только chmod 0600).
	encryptionKey []byte

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
// Security M3 (PASS-10): при невалидном BACKUP_ENCRYPTION_KEY возвращается
// ошибка (fail-closed) — раньше ключ игнорировался с Warn, и дампы писались
// в plaintext (секреты: пароли/2FA/refresh-хеши).
func NewBackupService(
	backupRepo BackupRepository,
	backupDir string,
	maxBackups int,
	dbCfg config.DatabaseConfig,
	encryptionKey string,
) (*BackupService, error) {
	if maxBackups <= 0 {
		maxBackups = 10
	}
	s := &BackupService{
		backupRepo: backupRepo,
		BackupDir:  backupDir,
		MaxBackups: maxBackups,
		dbHost:     dbCfg.Host,
		dbPort:     dbCfg.Port,
		dbUser:     dbCfg.User,
		dbPassword: dbCfg.Password,
		dbName:     dbCfg.Name,
	}
	// admin #6: ключ из env (hex 64 символа = 32 байта AES-256).
	if key, err := decodeBackupKey(encryptionKey); err != nil {
		return nil, fmt.Errorf("невалидный BACKUP_ENCRYPTION_KEY (ожидается 32 байта hex/base64): %w", err)
	} else if key != nil {
		s.encryptionKey = key
		log.Info().Msg("Backup: AES-256 encryption enabled for backups")
	}
	return s, nil
}

// decodeBackupKey разбирает BACKUP_ENCRYPTION_KEY: hex (64 символа) или
// base64 (44 символа) — всегда 32 байта. Пустая строка → nil (без шифрования).
func decodeBackupKey(raw string) ([]byte, error) {
	if raw == "" {
		return nil, nil
	}
	if decoded, err := hex.DecodeString(raw); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	return nil, fmt.Errorf("ожидается 32-байтный ключ (hex 64 или base64 44 символа)")
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
	// Security HIGH (PASS-10): директория бэкапов с секретами — 0700 (было 0755).
	if err := os.MkdirAll(s.BackupDir, 0700); err != nil {
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

	// Security HIGH (PASS-10): создаём файл с правами 0600 ДО pg_dump — иначе
	// pg_dump создаёт дамп с дефолтными правами (0644), и plaintext-файл с
	// хешами паролей/2FA-секретами читается любым локальным пользователем во
	// время многоминутного дампа (TOCTOU до старого chmod после).
	dumpFile, openErr := os.OpenFile(filepath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if openErr != nil {
		return fmt.Errorf("не удалось создать файл дампа с правами 0600: %w", openErr)
	}
	// Close после создания: права уже установлены, pg_dump перезапишет содержимое.
	if closeErr := dumpFile.Close(); closeErr != nil {
		return fmt.Errorf("не удалось закрыть файл дампа: %w", closeErr)
	}

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

	// admin #6 (PASS-8): шифруем дамп AES-256-GCM (если ключ настроен) —
	// plaintext-файл удаляется после шифрования.
	if encPath, encErr := s.encryptBackupFile(filepath); encErr != nil {
		return fmt.Errorf("не удалось зашифровать бэкап: %w", encErr)
	} else if encPath != filepath {
		filepath = encPath
		filename += ".enc"
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

// Download возвращает путь к файлу бекапа по ID и cleanup-функцию, которую
// ВЫЗЫВАЮЩИЙ обязан выполнить после отдачи (defer). Для зашифрованных бекапов
// возвращается временный расшифрованный файл — cleanup удаляет его, иначе
// plaintext-дамп с паролями/2FA-секретами остаётся на диске навсегда
// (security HIGH #1, PASS-10).
// SEC1: путь к файлу проверяется — он обязан лежать внутри BackupDir.
// Defense-in-depth: даже при компрометации записи в БД бекап-сервис не
// отдаст произвольный файл файловой системы.
func (s *BackupService) Download(ctx context.Context, backupID uint) (string, func(), error) {
	backup, err := s.backupRepo.GetByID(ctx, backupID)
	if err != nil {
		return "", nil, err
	}

	absBackupDir, err := filepath.Abs(s.BackupDir)
	if err != nil {
		return "", nil, fmt.Errorf("некорректная директория бекапов: %w", err)
	}
	absPath, err := filepath.Abs(backup.FilePath)
	if err != nil {
		return "", nil, fmt.Errorf("некорректный путь бекапа: %w", err)
	}
	rel, err := filepath.Rel(absBackupDir, absPath)
	if err != nil || rel == ".." || len(rel) >= 3 && rel[:3] == ".."+string(os.PathSeparator) {
		return "", nil, fmt.Errorf("путь бекапа выходит за пределы директории бекапов")
	}

	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return "", nil, fmt.Errorf("файл бекапа не найден")
	}

	// admin #6 (PASS-8): если бэкап зашифрован — расшифровываем во временный
	// файл; cleanup удалит его после отдачи (security HIGH #1, PASS-10).
	if strings.HasSuffix(absPath, ".enc") {
		plainPath, err := s.decryptBackupFile(absPath)
		if err != nil {
			return "", nil, err
		}
		return plainPath, func() {
			errors.LogSilently(os.Remove(plainPath), "Backup: failed to remove decrypted temp file")
		}, nil
	}
	return absPath, func() {}, nil
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

// encryptBackupFile (admin #6, PASS-8): шифрует файл AES-256-GCM на месте.
// Формат: 12-байт nonce || ciphertext. Возвращает путь к зашифрованному файлу.
func (s *BackupService) encryptBackupFile(srcPath string) (string, error) {
	if len(s.encryptionKey) != 32 {
		return srcPath, nil // шифрование не включено
	}
	plain, err := os.ReadFile(srcPath)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(s.encryptionKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, plain, nil)
	encPath := srcPath + ".enc"
	if err := os.WriteFile(encPath, ciphertext, 0600); err != nil {
		return "", err
	}
	// Удаляем незашифрованный дамп (plaintext не должен оставаться на диске).
	errors.LogSilently(os.Remove(srcPath), "Backup: failed to remove plaintext dump after encryption")
	return encPath, nil
}

// decryptBackupFile (admin #6, PASS-8): расшифровывает .enc в директории
// бекапов (для Download). Возвращает путь к временному расшифрованному файлу.
func (s *BackupService) decryptBackupFile(encPath string) (string, error) {
	if len(s.encryptionKey) != 32 {
		return encPath, nil // не зашифрован
	}
	data, err := os.ReadFile(encPath)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(s.encryptionKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", fmt.Errorf("зашифрованный бэкап повреждён (короткий файл)")
	}
	nonce, ciphertext := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("не удалось расшифровать бэкап: %w", err)
	}
	// Временный файл в директории бекапов с УНИКАЛЬНЫМ именем (M2, PASS-10) —
	// раньше `encPath + ".decrypted"` детерминирован: два параллельных Download
	// перезаписывали один файл (гона на c.File). cleanup удалит его после отдачи.
	plainPath := encPath + ".decrypted." + randomHex(8)
	if err := os.WriteFile(plainPath, plain, 0600); err != nil {
		return "", err
	}
	return plainPath, nil
}
