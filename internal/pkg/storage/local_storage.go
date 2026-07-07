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

// LocalStorage реализует FileStorage через локальную файловую систему.
type LocalStorage struct{}

// NewLocalStorage создаёт новый LocalStorage.
func NewLocalStorage() *LocalStorage {
	return &LocalStorage{}
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

	// Читаем первые 512 байт для проверки MIME-типа
	var header [512]byte
	n, err := io.ReadFull(reader, header[:])
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", fmt.Errorf("не удалось прочитать заголовок файла: %w", err)
	}

	// Проверка MIME-типа, если заданы разрешённые
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

	// Создаём директорию
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return "", fmt.Errorf("не удалось создать директорию %s: %w", baseDir, err)
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

	dst, err := os.Create(fullPath)
	if err != nil {
		return "", fmt.Errorf("не удалось создать файл: %w", err)
	}
	// Закрываем файл после записи, но если запись не удалась, удаляем его
	defer func() {
		_ = dst.Close()
		if err != nil {
			_ = os.Remove(fullPath)
		}
	}()

	if _, err := io.Copy(dst, dataReader); err != nil {
		return "", fmt.Errorf("не удалось записать файл: %w", err)
	}

	if maxSize > 0 {
		info, err := os.Stat(fullPath)
		if err != nil {
			return "", fmt.Errorf("не удалось проверить файл: %w", err)
		}
		if info.Size() > maxSize {
			return "", fmt.Errorf("размер файла превышает допустимый лимит %d байт", maxSize)
		}
	}

	return "/" + filepath.ToSlash(fullPath), nil
}

func (s *LocalStorage) Delete(webPath string) error {
	if webPath == "" {
		return nil
	}
	localPath := filepath.FromSlash(strings.TrimPrefix(webPath, "/"))
	absPath, err := filepath.Abs(localPath)
	if err != nil {
		return fmt.Errorf("не удалось получить абсолютный путь: %w", err)
	}
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return nil
	}
	return os.Remove(absPath)
}
