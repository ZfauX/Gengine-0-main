// internal/config/config_test.go
package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain устанавливает обязательные переменные окружения перед запуском любого теста,
// чтобы избежать ошибок при вызове LoadConfig в тестах.
func TestMain(m *testing.M) {
	_ = os.Setenv("DB_HOST", "localhost")
	_ = os.Setenv("DB_PORT", "5432")
	_ = os.Setenv("DB_USER", "test")
	_ = os.Setenv("DB_PASSWORD", "test")
	_ = os.Setenv("DB_NAME", "test")
	_ = os.Setenv("JWT_SECRET", "xK9mP2vL5nQ8wR3tY6uI0oP4sD7fG1hJ")
	_ = os.Setenv("SESSION_SECRET", "aB3cD4eF5gH6iJ7kL8mN9oP0qR1sT2uV3wX4yZ")
	_ = os.Setenv("ADMIN_EMAIL", "admin@test.com")
	_ = os.Setenv("ADMIN_PASSWORD", "securepassword12345")

	code := m.Run()
	os.Exit(code)
}

// =============================================================================
// Вспомогательные функции для тестов
// =============================================================================

// setEnv устанавливает переменную окружения и возвращает функцию для её восстановления.
func setEnv(t *testing.T, key, value string) func() {
	t.Helper()
	old, exists := os.LookupEnv(key)
	require.NoError(t, os.Setenv(key, value))
	return func() {
		if exists {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	}
}

// =============================================================================
// Тесты для LoadConfig (успешные сценарии)
// =============================================================================

func TestLoadConfig_Success(t *testing.T) {
	cleanup1 := setEnv(t, "DB_HOST", "localhost")
	defer cleanup1()
	cleanup2 := setEnv(t, "DB_PORT", "5432")
	defer cleanup2()
	cleanup3 := setEnv(t, "DB_USER", "testuser")
	defer cleanup3()
	cleanup4 := setEnv(t, "DB_PASSWORD", "testpass")
	defer cleanup4()
	cleanup5 := setEnv(t, "DB_NAME", "testdb")
	defer cleanup5()

	cleanup6 := setEnv(t, "JWT_SECRET", "xK9mP2vL5nQ8wR3tY6uI0oP4sD7fG1hJ")
	defer cleanup6()
	cleanup7 := setEnv(t, "SESSION_SECRET", "aB3cD4eF5gH6iJ7kL8mN9oP0qR1sT2uV3wX4yZ")
	defer cleanup7()
	cleanup8 := setEnv(t, "ADMIN_EMAIL", "admin@test.com")
	defer cleanup8()
	cleanup9 := setEnv(t, "ADMIN_PASSWORD", "securepassword12345")
	defer cleanup9()

	cfg, err := LoadConfig()
	require.NoError(t, err)

	assert.Equal(t, "localhost", cfg.Database.Host)
	assert.Equal(t, "5432", cfg.Database.Port)
	assert.Equal(t, "testuser", cfg.Database.User)
	assert.Equal(t, "testpass", cfg.Database.Password)
	assert.Equal(t, "testdb", cfg.Database.Name)
	assert.Equal(t, "disable", cfg.Database.SSLMode)
	assert.Equal(t, "xK9mP2vL5nQ8wR3tY6uI0oP4sD7fG1hJ", cfg.JWT.Secret)
	assert.Equal(t, "aB3cD4eF5gH6iJ7kL8mN9oP0qR1sT2uV3wX4yZ", cfg.Session.Secret)
	assert.Equal(t, "admin@test.com", cfg.Admin.Email)
	assert.Equal(t, "securepassword12345", cfg.Admin.Password)
}

func TestLoadConfig_WithOptionalEnv(t *testing.T) {
	cleanup1 := setEnv(t, "DB_HOST", "localhost")
	defer cleanup1()
	cleanup2 := setEnv(t, "DB_PORT", "5432")
	defer cleanup2()
	cleanup3 := setEnv(t, "DB_USER", "testuser")
	defer cleanup3()
	cleanup4 := setEnv(t, "DB_PASSWORD", "testpass")
	defer cleanup4()
	cleanup5 := setEnv(t, "DB_NAME", "testdb")
	defer cleanup5()
	cleanup6 := setEnv(t, "JWT_SECRET", "xK9mP2vL5nQ8wR3tY6uI0oP4sD7fG1hJ")
	defer cleanup6()
	cleanup7 := setEnv(t, "SESSION_SECRET", "aB3cD4eF5gH6iJ7kL8mN9oP0qR1sT2uV3wX4yZ")
	defer cleanup7()
	cleanup8 := setEnv(t, "ADMIN_EMAIL", "admin@test.com")
	defer cleanup8()
	cleanup9 := setEnv(t, "ADMIN_PASSWORD", "securepassword12345")
	defer cleanup9()

	cleanup10 := setEnv(t, "PORT", "9090")
	defer cleanup10()
	cleanup11 := setEnv(t, "GIN_MODE", "release")
	defer cleanup11()
	cleanup12 := setEnv(t, "BASE_URL", "https://example.com")
	defer cleanup12()
	cleanup13 := setEnv(t, "DB_SSLMODE", "require")
	defer cleanup13()
	cleanup14 := setEnv(t, "JWT_ACCESS_EXPIRY", "30m")
	defer cleanup14()

	cfg, err := LoadConfig()
	require.NoError(t, err)

	assert.Equal(t, "9090", cfg.Server.Port)
	assert.Equal(t, "release", cfg.Server.GinMode)
	assert.Equal(t, "https://example.com", cfg.Server.BaseURL)
	assert.Equal(t, "require", cfg.Database.SSLMode)
	assert.Equal(t, 30*time.Minute, cfg.JWT.AccessExpiry)
}

func TestLoadConfig_OAuthEnabled(t *testing.T) {
	cleanup1 := setEnv(t, "DB_HOST", "localhost")
	defer cleanup1()
	cleanup2 := setEnv(t, "DB_PORT", "5432")
	defer cleanup2()
	cleanup3 := setEnv(t, "DB_USER", "u")
	defer cleanup3()
	cleanup4 := setEnv(t, "DB_PASSWORD", "p")
	defer cleanup4()
	cleanup5 := setEnv(t, "DB_NAME", "d")
	defer cleanup5()
	cleanup6 := setEnv(t, "JWT_SECRET", "xK9mP2vL5nQ8wR3tY6uI0oP4sD7fG1hJ")
	defer cleanup6()
	cleanup7 := setEnv(t, "SESSION_SECRET", "aB3cD4eF5gH6iJ7kL8mN9oP0qR1sT2uV3wX4yZ")
	defer cleanup7()
	cleanup8 := setEnv(t, "ADMIN_EMAIL", "a@b.c")
	defer cleanup8()
	cleanup9 := setEnv(t, "ADMIN_PASSWORD", "securepassword12345")
	defer cleanup9()

	cleanup10 := setEnv(t, "GOOGLE_ENABLED", "true")
	defer cleanup10()
	cleanup11 := setEnv(t, "GOOGLE_CLIENT_ID", "google_id")
	defer cleanup11()
	cleanup12 := setEnv(t, "GOOGLE_CLIENT_SECRET", "google_secret")
	defer cleanup12()

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.True(t, cfg.OAuth.Google.Enabled)
	assert.Equal(t, "google_id", cfg.OAuth.Google.ClientID)
	assert.Equal(t, "google_secret", cfg.OAuth.Google.ClientSecret)
}

func TestLoadConfig_SMTPEnabled(t *testing.T) {
	cleanup1 := setEnv(t, "DB_HOST", "localhost")
	defer cleanup1()
	cleanup2 := setEnv(t, "DB_PORT", "5432")
	defer cleanup2()
	cleanup3 := setEnv(t, "DB_USER", "u")
	defer cleanup3()
	cleanup4 := setEnv(t, "DB_PASSWORD", "p")
	defer cleanup4()
	cleanup5 := setEnv(t, "DB_NAME", "d")
	defer cleanup5()
	cleanup6 := setEnv(t, "JWT_SECRET", "xK9mP2vL5nQ8wR3tY6uI0oP4sD7fG1hJ")
	defer cleanup6()
	cleanup7 := setEnv(t, "SESSION_SECRET", "aB3cD4eF5gH6iJ7kL8mN9oP0qR1sT2uV3wX4yZ")
	defer cleanup7()
	cleanup8 := setEnv(t, "ADMIN_EMAIL", "a@b.c")
	defer cleanup8()
	cleanup9 := setEnv(t, "ADMIN_PASSWORD", "securepassword12345")
	defer cleanup9()

	cleanup10 := setEnv(t, "SMTP_ENABLED", "true")
	defer cleanup10()
	cleanup11 := setEnv(t, "SMTP_HOST", "smtp.test.com")
	defer cleanup11()
	cleanup12 := setEnv(t, "SMTP_PORT", "587")
	defer cleanup12()
	cleanup13 := setEnv(t, "SMTP_USER", "user")
	defer cleanup13()
	cleanup14 := setEnv(t, "SMTP_PASSWORD", "pass")
	defer cleanup14()
	cleanup15 := setEnv(t, "SMTP_FROM", "from@test.com")
	defer cleanup15()

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.True(t, cfg.SMTP.Enabled)
	assert.Equal(t, "smtp.test.com", cfg.SMTP.Host)
	assert.Equal(t, 587, cfg.SMTP.Port)
	assert.Equal(t, "user", cfg.SMTP.User)
	assert.Equal(t, "pass", cfg.SMTP.Password)
	assert.Equal(t, "from@test.com", cfg.SMTP.From)
}

func TestLoadConfig_ReCAPTCHAEnabled(t *testing.T) {
	cleanup1 := setEnv(t, "DB_HOST", "localhost")
	defer cleanup1()
	cleanup2 := setEnv(t, "DB_PORT", "5432")
	defer cleanup2()
	cleanup3 := setEnv(t, "DB_USER", "u")
	defer cleanup3()
	cleanup4 := setEnv(t, "DB_PASSWORD", "p")
	defer cleanup4()
	cleanup5 := setEnv(t, "DB_NAME", "d")
	defer cleanup5()
	cleanup6 := setEnv(t, "JWT_SECRET", "xK9mP2vL5nQ8wR3tY6uI0oP4sD7fG1hJ")
	defer cleanup6()
	cleanup7 := setEnv(t, "SESSION_SECRET", "aB3cD4eF5gH6iJ7kL8mN9oP0qR1sT2uV3wX4yZ")
	defer cleanup7()
	cleanup8 := setEnv(t, "ADMIN_EMAIL", "a@b.c")
	defer cleanup8()
	cleanup9 := setEnv(t, "ADMIN_PASSWORD", "securepassword12345")
	defer cleanup9()

	cleanup10 := setEnv(t, "RECAPTCHA_ENABLED", "true")
	defer cleanup10()
	cleanup11 := setEnv(t, "RECAPTCHA_SITE_KEY", "sitekey")
	defer cleanup11()
	cleanup12 := setEnv(t, "RECAPTCHA_SECRET_KEY", "secretkey")
	defer cleanup12()

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.True(t, cfg.ReCAPTCHA.Enabled)
	assert.Equal(t, "sitekey", cfg.ReCAPTCHA.SiteKey)
	assert.Equal(t, "secretkey", cfg.ReCAPTCHA.SecretKey)
}

// =============================================================================
// Тесты ошибочных ситуаций (прямая проверка ошибок)
// =============================================================================

func TestLoadConfig_MissingRequired(t *testing.T) {
	// Удаляем обязательную переменную и проверяем ошибку
	cleanup := setEnv(t, "DB_HOST", "") // пустое значение
	defer cleanup()
	// Также нужно убедиться, что другие переменные установлены (уже есть в TestMain)
	// Но DB_HOST должен быть пустым
	_, err := LoadConfig()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "DB_HOST")
}

func TestLoadConfig_JWTSecretTooShort(t *testing.T) {
	cleanup := setEnv(t, "JWT_SECRET", "short")
	defer cleanup()
	_, err := LoadConfig()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "JWT_SECRET")
	assert.Contains(t, err.Error(), "at least 32 characters")
}

func TestLoadConfig_JWTSecretWeak(t *testing.T) {
	cleanup := setEnv(t, "JWT_SECRET", "change-me-12345678901234567890ab")
	defer cleanup()
	_, err := LoadConfig()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "weak/default value")
}

func TestLoadConfig_OAuthEnabledMissingClientID(t *testing.T) {
	cleanup1 := setEnv(t, "GITHUB_ENABLED", "true")
	defer cleanup1()
	cleanup2 := setEnv(t, "GITHUB_CLIENT_ID", "") // пусто
	defer cleanup2()
	cleanup3 := setEnv(t, "GITHUB_CLIENT_SECRET", "") // пусто
	defer cleanup3()
	_, err := LoadConfig()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "GITHUB")
}

func TestLoadConfig_SMTPEnabledMissingFrom(t *testing.T) {
	cleanup1 := setEnv(t, "SMTP_ENABLED", "true")
	defer cleanup1()
	cleanup2 := setEnv(t, "SMTP_HOST", "smtp.example.com")
	defer cleanup2()
	cleanup3 := setEnv(t, "SMTP_FROM", "") // пусто
	defer cleanup3()
	_, err := LoadConfig()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SMTP_FROM")
}

func TestLoadConfig_InvalidDuration(t *testing.T) {
	cleanup := setEnv(t, "JWT_ACCESS_EXPIRY", "invalid")
	defer cleanup()
	_, err := LoadConfig()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid duration")
}

// =============================================================================
// Бенчмарк для LoadConfig
// =============================================================================

func BenchmarkLoadConfig(b *testing.B) {
	_ = os.Setenv("DB_HOST", "localhost")
	_ = os.Setenv("DB_PORT", "5432")
	_ = os.Setenv("DB_USER", "user")
	_ = os.Setenv("DB_PASSWORD", "pass")
	_ = os.Setenv("DB_NAME", "db")
	_ = os.Setenv("JWT_SECRET", "xK9mP2vL5nQ8wR3tY6uI0oP4sD7fG1hJ")
	_ = os.Setenv("SESSION_SECRET", "aB3cD4eF5gH6iJ7kL8mN9oP0qR1sT2uV3wX4yZ")
	_ = os.Setenv("ADMIN_EMAIL", "a@b.c")
	_ = os.Setenv("ADMIN_PASSWORD", "securepassword12345")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = LoadConfig()
	}
}
