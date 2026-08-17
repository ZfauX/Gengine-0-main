// internal/domain/admin/encrypt_test.go
package admin

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// newTestBackupServiceWithKey создаёт BackupService с включённым шифрованием
// (32-байтный случайный ключ) — без БД, только для encrypt/decrypt round-trip.
func newTestBackupServiceWithKey(t *testing.T) *BackupService {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	return &BackupService{encryptionKey: key}
}

// TestBackupEncryptDecrypt_RoundTrip (H1, PASS-21): encrypt → decrypt даёт
// исходный контент. Раньше cipher.NewCTR(block, gcm.NonceSize()=12) паниковал
// ("incorrect IV length") — тест ловит регресс.
func TestBackupEncryptDecrypt_RoundTrip(t *testing.T) {
	s := newTestBackupServiceWithKey(t)

	dir := t.TempDir()
	src := filepath.Join(dir, "dump.sql")
	content := bytes.Repeat([]byte("CREATE TABLE users (...);\n"), 10000) // ~270KB
	require.NoError(t, os.WriteFile(src, content, 0600))

	encPath, err := s.encryptBackupFile(src)
	require.NoError(t, err)
	require.FileExists(t, encPath)
	// Исходник удаляется после шифрования (plaintext не остаётся).
	require.NoFileExists(t, src)

	encData, err := os.ReadFile(encPath)
	require.NoError(t, err)
	// Формат: 16-байт IV + 32-байт HMAC + ciphertext.
	require.Greater(t, len(encData), 16+32)
	require.NotEqual(t, content, encData, "ciphertext must differ from plaintext")

	plainPath, err := s.decryptBackupFile(encPath)
	require.NoError(t, err)
	defer os.Remove(plainPath)

	plain, err := os.ReadFile(plainPath)
	require.NoError(t, err)
	require.Equal(t, content, plain)
}

// TestBackupDecrypt_TamperedHMAC (M1, PASS-21): изменение ciphertext должно
// приводить к ошибке (HMAC mismatch), а не молчаливому мусору.
func TestBackupDecrypt_TamperedHMAC(t *testing.T) {
	s := newTestBackupServiceWithKey(t)

	dir := t.TempDir()
	src := filepath.Join(dir, "dump.sql")
	require.NoError(t, os.WriteFile(src, []byte("sensitive data"), 0600))

	encPath, err := s.encryptBackupFile(src)
	require.NoError(t, err)

	encData, err := os.ReadFile(encPath)
	require.NoError(t, err)
	// Портим один байт ciphertext (после IV+HMAC).
	encData[len(encData)-1] ^= 0xFF
	require.NoError(t, os.WriteFile(encPath, encData, 0600))

	plainPath, err := s.decryptBackupFile(encPath)
	require.Error(t, err, "tampered ciphertext must be rejected")
	require.Empty(t, plainPath)
	require.NoFileExists(t, plainPath, "temporary plaintext must be cleaned up")
}
