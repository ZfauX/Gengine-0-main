// internal/pkg/storage/local_storage.go
package storage

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	fileHeaderReadBytes = 512
	defaultMaxFileSize  = 50 * 1024 * 1024
)

// LocalStorage реализует FileStorage через локальную файловую систему.
type LocalStorage struct {
	baseDir string
}

// NewLocalStorage создаёт новый LocalStorage.
func NewLocalStorage() *LocalStorage {
	return &LocalStorage{}
}

// WithBaseDir задаёт базовую директорию для хранилища.
func (s *LocalStorage) WithBaseDir(baseDir string) *LocalStorage {
	s.baseDir = baseDir
	return s
}

var safeFilenameRE = regexp.MustCompile(`[^a-zA-Z0-9.\-]`)

// sanitizeFilename очищает имя файла, оставляя только безопасные символы.
func sanitizeFilename(name string) string {
	if name == "" {
		return ""
	}
	clean := safeFilenameRE.ReplaceAllString(name, "_")
	clean = filepath.Base(clean)
	if clean == "." || clean == "" {
		return ""
	}
	return clean
}

// validateExtension проверяет, что расширение файла допустимо.
var allowedExtensions = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".webp": true,
	".gif":  true,
	".pdf":  true,
	".txt":  true,
	".bin":  true,
}

func validateExtension(ext string) bool {
	return allowedExtensions[strings.ToLower(ext)]
}

// randomHex возвращает криптостойкую hex-строку длиной 2*n символов.
// Используется для nonce в имени сохраняемого файла (DEEP-REVIEW PASS-2 #22):
// предсказуемое имя {userID}_{unixnano} заменено на случайное, чтобы внешний
// наблюдатель не мог перечислить/угадать пути чужих файлов.
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// Крайне маловероятный fallback — не блокируем запись из-за энтропии.
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func (s *LocalStorage) Save(baseDir string, reader io.Reader, originalName string, userID uint, maxSize int64, allowedMIMETypes []string) (string, error) {
	// Защита от path traversal на уровне исходного имени
	if strings.Contains(originalName, "..") || filepath.IsAbs(originalName) {
		return "", fmt.Errorf("недопустимое имя файла")
	}

	safeName := sanitizeFilename(originalName)
	ext := filepath.Ext(safeName)
	if ext == "" {
		ext = ".bin"
	}

	// Дополнительная проверка расширения файла
	if !validateExtension(ext) {
		return "", fmt.Errorf("недопустимое расширение файла: %s", ext)
	}

	var header [fileHeaderReadBytes]byte
	n, err := io.ReadFull(reader, header[:])
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", fmt.Errorf("не удалось прочитать заголовок файла")
	}

	// Проверка MIME-типа, если заданы разрешённые
	// NOTE: http.DetectContentType определяет WebP как image/webp
	if len(allowedMIMETypes) > 0 {
		contentType := http.DetectContentType(header[:n])
		// Убираем параметры (charset и т.п.)
		contentTypeBase, _, _ := strings.Cut(contentType, ";")
		allowed := false
		for _, t := range allowedMIMETypes {
			tBase, _, _ := strings.Cut(t, ";")
			if contentTypeBase == tBase {
				allowed = true
				break
			}
		}
		if !allowed {
			return "", fmt.Errorf("недопустимый тип файла: %s", contentType)
		}
	}

	// Создаём reader, который сначала отдаст заголовок, потом остаток исходного потока
	dataReader := io.MultiReader(bytes.NewReader(header[:n]), reader)

	// Ограничиваем размер на уровне reader — предотвращает переполнение диска
	if maxSize <= 0 {
		maxSize = defaultMaxFileSize
	}
	dataReader = io.LimitReader(dataReader, maxSize+1) // +1 чтобы обнаружить превышение

	// DEEP-REVIEW PASS-2 (#22): права директории 0700 (не 0755) —
	// файлы не должны быть доступны другим пользователям системы.
	if mkdirErr := os.MkdirAll(baseDir, 0700); mkdirErr != nil {
		return "", fmt.Errorf("не удалось создать директорию для загрузки: %w", mkdirErr)
	}

	// DEEP-REVIEW PASS-2 (#22): случайный nonce вместо предсказуемого unixnano.
	filename := fmt.Sprintf("%d_%s%s", userID, randomHex(8), ext)
	fullPath := filepath.Join(baseDir, filename)

	// Проверка выхода за пределы директории (защита от симлинков/подмены).
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("некорректная базовая директория: %w", err)
	}
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("некорректный путь файла: %w", err)
	}
	rel, err := filepath.Rel(absBase, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("путь файла выходит за пределы директории хранения")
	}

	// DEEP-REVIEW PASS-2 (#22): права файла 0600 (не 0666/os.Create) —
	// загруженные файлы читает только процесс сервера (c.File).
	dst, createErr := os.OpenFile(fullPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if createErr != nil {
		return "", fmt.Errorf("не удалось создать файл")
	}

	var writeErr error
	defer func() {
		if closeErr := dst.Close(); closeErr != nil && writeErr == nil && createErr == nil {
			log.Error().Err(closeErr).Str("path", fullPath).Msg("Save: file close failed")
		}
		if writeErr != nil || createErr != nil {
			if removeErr := os.Remove(fullPath); removeErr != nil {
				log.Warn().Err(removeErr).Str("path", fullPath).Msg("Save: cleanup file failed")
			}
		}
	}()

	var written int64
	written, err = io.Copy(dst, dataReader)
	if err != nil && err != io.EOF {
		writeErr = err
		return "", fmt.Errorf("не удалось записать файл")
	}

	// Проверяем, не превышен ли лимит (io.LimitReader может вернуть io.EOF раньше)
	if written > maxSize {
		if err := dst.Close(); err != nil {
			log.Error().Err(err).Str("path", fullPath).Msg("Save: file close failed on size limit")
		}
		if err := os.Remove(fullPath); err != nil {
			log.Warn().Err(err).Str("path", fullPath).Msg("Save: cleanup file failed on size limit")
		}
		return "", fmt.Errorf("размер файла превышает допустимый лимит %d байт", maxSize)
	}

	return "/" + filepath.ToSlash(fullPath), nil
}

func (s *LocalStorage) Delete(webPath string) error {
	if webPath == "" {
		return nil
	}

	// Path traversal protection: запрещаем ".."
	if strings.Contains(webPath, "..") {
		return fmt.Errorf("путь файла выходит за пределы директории загрузок")
	}

	// webPath — путь, возвращённый Save. Он бывает двух видов:
	//   1. Веб-путь "/uploads/photos/x.jpg" (прод): это НЕ абсолютный ФС-путь,
	//      а путь ОТНОСИТЕЛЬНО s.baseDir. Раньше он трактовался как абсолютный
	//      "/uploads/..." на корне диска, boundary-проверка всегда падала, и
	//      файлы в проде никогда не удалялись с диска (DEEP-REVIEW #16).
	//   2. Абсолютный ФС-путь с ведущим "/" ("//tmp/..." на Unix, "/C:/..." на
	//      Windows) или без него ("C:/...") — из тестов/легаси.
	// Распознаём по порядку: сначала абсолютный с удвоенным слэшем, затем веб-путь
	// с префиксом baseName(s.baseDir), затем прочие абсолютные, затем относительные.
	sl := filepath.ToSlash(webPath)

	var fullPath string
	switch {
	case strings.HasPrefix(sl, "/") && filepath.IsAbs(filepath.FromSlash(sl[1:])):
		// "//tmp/..." или "/C:/..." — абсолютный ФС-путь с удвоенным ведущим "/".
		fullPath = filepath.FromSlash(sl[1:])
	case s.baseDir != "" && strings.HasPrefix(sl, "/"+filepath.ToSlash(filepath.Base(filepath.Clean(s.baseDir)))+"/"):
		// Веб-путь "/uploads/photos/x.jpg" — резолвим относительно s.baseDir,
		// снимая совпадающий префикс базовой директории (иначе было бы
		// "uploads/uploads/photos/x.jpg").
		prefix := "/" + filepath.ToSlash(filepath.Base(filepath.Clean(s.baseDir))) + "/"
		rel := strings.TrimPrefix(sl, prefix)
		fullPath = filepath.Join(s.baseDir, filepath.FromSlash(rel))
	case filepath.IsAbs(filepath.FromSlash(sl)):
		// Абсолютный ФС-путь (Windows "C:/...", Unix "/tmp/...") — легаси/тесты.
		fullPath = filepath.FromSlash(sl)
	default:
		// Относительный путь без ведущего "/" (легаси/веб без префикса).
		fullPath = filepath.Join(s.baseDir, filepath.FromSlash(sl))
	}

	// Проверка выхода за пределы директории
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return fmt.Errorf("некорректный путь файла: %w", err)
	}
	if s.baseDir != "" {
		absBase, err := filepath.Abs(s.baseDir)
		if err != nil {
			return fmt.Errorf("некорректная базовая директория: %w", err)
		}
		// Точная проверка границы директории: файл обязан лежать внутри baseDir.
		// filepath.Rel корректно обрабатывает разделители и префиксы на всех ОС,
		// в отличие от простого HasPrefix (который пропустил бы "/tmp/storage_evil").
		rel, err := filepath.Rel(absBase, absPath)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("путь файла выходит за пределы директории хранения")
		}
	}
	// NB: при baseDir=="" граница не проверяется. В проде s.baseDir всегда задаётся
	// через WithBaseDir (cmd/server/main.go), поэтому произвольное удаление
	// системных файлов через публичный API невозможно. DEEP-REVIEW PASS-2 #9:
	// задокументировано как LOW (требует редизайна Delete для приёма baseDir).

	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return nil
	}
	return os.Remove(absPath)
}
