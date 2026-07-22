// internal/pkg/storage/local_storage.go
package storage

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
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

// sanitizeFilename очищает имя файла, оставляя только безопасные символы.
func sanitizeFilename(name string) string {
	if name == "" {
		return ""
	}
	reg := regexp.MustCompile(`[^a-zA-Z0-9.\-]`)
	clean := reg.ReplaceAllString(name, "_")
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

	// Создаём директорию
	if mkdirErr := os.MkdirAll(baseDir, 0755); mkdirErr != nil {
		return "", fmt.Errorf("не удалось создать директорию для загрузки: %w", mkdirErr)
	}

	filename := fmt.Sprintf("%d_%d%s", userID, time.Now().UnixNano(), ext)
	fullPath := filepath.Join(baseDir, filename)

	// Проверка выхода за пределы директории
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("некорректная базовая директория: %w", err)
	}
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("некорректный путь файла: %w", err)
	}
	if !strings.HasPrefix(absPath, absBase) {
		return "", fmt.Errorf("путь файла выходит за пределы директории хранения")
	}

	dst, createErr := os.Create(fullPath)
	if createErr != nil {
		return "", fmt.Errorf("не удалось создать файл")
	}
	// Закрываем файл после записи, но если запись не удалась, удаляем его
	fileErr := createErr
	defer func() {
		_ = dst.Close()
		if fileErr != nil {
			_ = os.Remove(fullPath)
		}
	}()

	var written int64
	written, err = io.Copy(dst, dataReader)
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("не удалось записать файл")
	}

	// Проверяем, не превышен ли лимит (io.LimitReader может вернуть io.EOF раньше)
	if written > maxSize {
		_ = dst.Close()
		_ = os.Remove(fullPath)
		return "", fmt.Errorf("размер файла превышает допустимый лимит %d байт", maxSize)
	}

	return "/" + filepath.ToSlash(fullPath), nil
}

func (s *LocalStorage) Delete(webPath string) error {
	if webPath == "" {
		return nil
	}
	// webPath — абсолютный путь, возвращённый Save (например, /abs/path/to/file.jpg)
	relPath := webPath
	if strings.HasPrefix(webPath, "/") {
		relPath = webPath[1:]
	}

	// Path traversal protection: запрещаем ".." в относительной части пути
	if strings.Contains(relPath, "..") {
		return fmt.Errorf("путь файла выходит за пределы директории загрузок")
	}

	fullPath := filepath.FromSlash(relPath)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		return nil
	}
	return os.Remove(fullPath)
}
