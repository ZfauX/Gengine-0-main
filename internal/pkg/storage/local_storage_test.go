// internal/pkg/storage/local_storage_test.go
package storage

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalStorage_Save(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "storage_test")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	storage := NewLocalStorage()
	userID := uint(42)
	originalName := "test.txt"
	content := []byte("hello world")

	t.Run("успешное сохранение", func(t *testing.T) {
		reader := bytes.NewReader(content)
		path, err := storage.Save(tmpDir, reader, originalName, userID, 0, nil)
		require.NoError(t, err)
		assert.NotEmpty(t, path)
		assert.Contains(t, path, "/")

		fullPath := filepath.FromSlash(strings.TrimPrefix(path, "/"))
		info, err := os.Stat(fullPath)
		require.NoError(t, err)
		assert.Greater(t, info.Size(), int64(0))

		data, err := os.ReadFile(fullPath)
		require.NoError(t, err)
		assert.Equal(t, content, data)
	})

	t.Run("сохранение с ограничением размера", func(t *testing.T) {
		reader := bytes.NewReader(content)
		_, err := storage.Save(tmpDir, reader, originalName, userID, 5, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "превышает допустимый лимит")
	})

	t.Run("сохранение с разрешёнными MIME-типами", func(t *testing.T) {
		reader := bytes.NewReader(content)
		allowed := []string{"text/plain"}
		path, err := storage.Save(tmpDir, reader, originalName, userID, 0, allowed)
		require.NoError(t, err)
		assert.NotEmpty(t, path)
	})

	t.Run("сохранение с неподдерживаемым MIME-типом", func(t *testing.T) {
		pngData := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
		reader := bytes.NewReader(pngData)
		allowed := []string{"text/plain"}
		_, err := storage.Save(tmpDir, reader, "image.png", userID, 0, allowed)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "недопустимый тип файла")
	})

	t.Run("сохранение с пустым именем", func(t *testing.T) {
		reader := bytes.NewReader(content)
		path, err := storage.Save(tmpDir, reader, "", userID, 0, nil)
		require.NoError(t, err)
		assert.Contains(t, path, ".bin")
	})

	t.Run("сохранение с именем, содержащим недопустимые символы", func(t *testing.T) {
		reader := bytes.NewReader(content)
		unsafeName := "../../../etc/passwd"
		_, err := storage.Save(tmpDir, reader, unsafeName, userID, 0, nil)
		assert.Error(t, err, "должна быть ошибка при path traversal")
	})

	t.Run("сохранение в несуществующую поддиректорию", func(t *testing.T) {
		subDir := filepath.Join(tmpDir, "sub", "dir")
		reader := bytes.NewReader(content)
		path, err := storage.Save(subDir, reader, originalName, userID, 0, nil)
		require.NoError(t, err)
		assert.NotEmpty(t, path)

		_, err = os.Stat(subDir)
		assert.NoError(t, err)
	})
}

func TestLocalStorage_Delete(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "storage_delete_test")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	storage := NewLocalStorage()
	userID := uint(1)
	content := []byte("delete me")

	reader := bytes.NewReader(content)
	path, err := storage.Save(tmpDir, reader, "to_delete.txt", userID, 0, nil)
	require.NoError(t, err)
	fullPath := filepath.FromSlash(strings.TrimPrefix(path, "/"))

	t.Run("успешное удаление", func(t *testing.T) {
		err := storage.Delete(path)
		require.NoError(t, err)

		_, err = os.Stat(fullPath)
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("удаление уже удалённого файла", func(t *testing.T) {
		err := storage.Delete(path)
		require.NoError(t, err)
	})

	t.Run("удаление с пустым путём", func(t *testing.T) {
		err := storage.Delete("")
		require.NoError(t, err)
	})

	t.Run("удаление с несуществующим путём", func(t *testing.T) {
		err := storage.Delete("/nonexistent/file.txt")
		require.NoError(t, err)
	})

	t.Run("безопасность: удаление файла за пределами временной директории", func(t *testing.T) {
		// Создаём временный файл, записываем данные, закрываем, удаляем через storage.Delete
		tmpFile, err := os.CreateTemp("", "delete_test_*")
		require.NoError(t, err)
		defer func() {
			// На случай, если Delete не удалит, уберём сами
			_ = os.Remove(tmpFile.Name())
		}()
		// Сразу закрываем, чтобы storage.Delete мог удалить
		err = tmpFile.Close()
		require.NoError(t, err)

		err = storage.Delete(tmpFile.Name())
		require.NoError(t, err)
		_, err = os.Stat(tmpFile.Name())
		assert.True(t, os.IsNotExist(err))
	})
}

func TestLocalStorage_Save_Security_PathTraversal(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "storage_security_test")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	storage := NewLocalStorage()
	userID := uint(1)
	content := []byte("test")

	reader := bytes.NewReader(content)
	_, err = storage.Save(tmpDir, reader, "../../../outside.txt", userID, 0, nil)
	require.Error(t, err, "должна быть ошибка при path traversal")
}

func TestLocalStorage_Save_EmptyReader(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "storage_empty_test")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	storage := NewLocalStorage()
	reader := bytes.NewReader([]byte{})
	path, err := storage.Save(tmpDir, reader, "empty.txt", 1, 0, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, path)

	fullPath := filepath.FromSlash(strings.TrimPrefix(path, "/"))
	info, err := os.Stat(fullPath)
	require.NoError(t, err)
	assert.Equal(t, int64(0), info.Size())
}

func TestLocalStorage_Save_LargeFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "storage_large_test")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	storage := NewLocalStorage()
	data := bytes.Repeat([]byte("a"), 10*1024*1024)
	reader := bytes.NewReader(data)

	path, err := storage.Save(tmpDir, reader, "large.bin", 1, 0, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, path)

	fullPath := filepath.FromSlash(strings.TrimPrefix(path, "/"))
	info, err := os.Stat(fullPath)
	require.NoError(t, err)
	assert.Equal(t, int64(10*1024*1024), info.Size())
}

func BenchmarkLocalStorage_Save(b *testing.B) {
	tmpDir, err := os.MkdirTemp("", "storage_bench")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	storage := NewLocalStorage()
	data := []byte("hello world")
	userID := uint(1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reader := bytes.NewReader(data)
		_, _ = storage.Save(tmpDir, reader, "bench.txt", userID, 0, nil)
	}
}

func TestLocalStorage_ImplementsInterface(t *testing.T) {
	var _ FileStorage = (*LocalStorage)(nil)
}
