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

type LocalStorage struct{}

func NewLocalStorage() *LocalStorage {
	return &LocalStorage{}
}

func sanitizeFilename(name string) string {
	reg := regexp.MustCompile(`[^a-zA-Z0-9.\-]`)
	clean := reg.ReplaceAllString(name, "_")
	return filepath.Base(clean)
}

func (s *LocalStorage) Save(baseDir string, reader io.Reader, originalName string, userID uint, maxSize int64, allowedMIMETypes []string) (string, error) {
	safeName := sanitizeFilename(originalName)
	ext := filepath.Ext(safeName)
	if ext == "" {
		ext = ".bin"
	}

	// Читаем первые 512 байт для проверки MIME-типа
	var header [512]byte
	n, err := io.ReadFull(reader, header[:])
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", fmt.Errorf("не удалось прочитать заголовок файла: %w", err)
	}

	// Проверка MIME типа, если заданы разрешённые
	if len(allowedMIMETypes) > 0 {
		contentType := http.DetectContentType(header[:n])
		allowed := false
		for _, allowedType := range allowedMIMETypes {
			if contentType == allowedType {
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
	defer func() { _ = dst.Close() }()

	if _, err := io.Copy(dst, dataReader); err != nil {
		_ = os.Remove(fullPath)
		return "", fmt.Errorf("не удалось записать файл: %w", err)
	}

	if maxSize > 0 {
		info, err := os.Stat(fullPath)
		if err != nil {
			_ = os.Remove(fullPath)
			return "", fmt.Errorf("не удалось проверить файл: %w", err)
		}
		if info.Size() > maxSize {
			_ = os.Remove(fullPath)
			return "", fmt.Errorf("размер файла превышает допустимый лимит %d байт", maxSize)
		}
	}

	return "/" + filepath.ToSlash(fullPath), nil
}

func (s *LocalStorage) Delete(webPath string) error {
	if webPath == "" {
		return nil
	}
	// Убираем ведущий слеш и конвертируем в локальный разделитель
	localPath := filepath.FromSlash(strings.TrimPrefix(webPath, "/"))
	absPath, err := filepath.Abs(localPath)
	if err != nil {
		return fmt.Errorf("не удалось получить абсолютный путь: %w", err)
	}
	// Если файла нет, не считаем это ошибкой
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return nil
	}
	return os.Remove(absPath)
}