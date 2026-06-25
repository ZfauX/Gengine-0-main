// internal/config/config_test.go
package config

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain устанавливает обязательные переменные окружения перед запуском любого теста,
// чтобы избежать фатального завершения LoadConfig при её неявном вызове.
func TestMain(m *testing.M) {
	_ = os.Setenv("DB_HOST", "localhost")
	_ = os.Setenv("DB_PORT", "5432")
	_ = os.Setenv("DB_USER", "test")
	_ = os.Setenv("DB_PASSWORD", "test")
	_ = os.Setenv("DB_NAME", "test")
	_ = os.Setenv("JWT_SECRET", "supersecretkeywithatleast32chars")
	_ = os.Setenv("SESSION_SECRET", "sessionsupersecretkey32charslong!!")
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
// Вспомогательный механизм для тестирования фатальных ошибок (log.Fatal)
// =============================================================================

const (
	testCaseMissingRequired = "MISSING_REQUIRED"
	testCaseJWTShort        = "JWT_SHORT"
	testCaseJWTWeak         = "JWT_WEAK"
	testCaseOAuthMissing    = "OAUTH_MISSING"
	testCaseSMTPMissingFrom = "SMTP_MISSING_FROM"
	testCaseInvalidDuration = "INVALID_DURATION"
)

// TestFatalHelper – тест, вызываемый только в подпроцессе для проверки фатальных ситуаций.
// Окружение настраивается через переменную CONFIG_TEST_CASE.
func TestFatalHelper(t *testing.T) {
	testCase := os.Getenv("CONFIG_TEST_CASE")
	if testCase == "" {
		t.Skip("helper test, only runs in subprocess")
	}

	switch testCase {
	case testCaseMissingRequired:
		_ = os.Unsetenv("DB_HOST")
	case testCaseJWTShort:
		_ = os.Setenv("JWT_SECRET", "short")
	case testCaseJWTWeak:
		_ = os.Setenv("JWT_SECRET", "change-me")
	case testCaseOAuthMissing:
		_ = os.Setenv("GITHUB_ENABLED", "true")
		_ = os.Unsetenv("GITHUB_CLIENT_ID")
		_ = os.Unsetenv("GITHUB_CLIENT_SECRET")
	case testCaseSMTPMissingFrom:
		_ = os.Setenv("SMTP_ENABLED", "true")
		_ = os.Setenv("SMTP_HOST", "smtp.example.com")
		_ = os.Setenv("SMTP_PORT", "587")
		_ = os.Setenv("SMTP_USER", "user")
		_ = os.Setenv("SMTP_PASSWORD", "pass")
		_ = os.Unsetenv("SMTP_FROM")
	case testCaseInvalidDuration:
		_ = os.Setenv("JWT_ACCESS_EXPIRY", "invalid")
	default:
		t.Fatalf("unknown test case: %s", testCase)
	}

	// Этот вызов должен привести к log.Fatal -> os.Exit(1)
	LoadConfig()
	t.Fatal("expected fatal exit, but LoadConfig returned normally")
}

// runFatalSubtest запускает текущий тестовый бинарник в подпроцессе
// и ожидает, что процесс завершится с кодом 1.
// Вывод подпроцесса подавляется, чтобы не засорять логи тестов.
func runFatalSubtest(t *testing.T, testCase string, removeKeys []string, extraEnv map[string]string) {
	t.Helper()

	// Копируем окружение, исключая указанные ключи
	env := os.Environ()
	var filtered []string
	for _, e := range env {
		keep := true
		key := strings.SplitN(e, "=", 2)[0]
		for _, rm := range removeKeys {
			if key == rm {
				keep = false
				break
			}
		}
		if keep {
			filtered = append(filtered, e)
		}
	}
	filtered = append(filtered, "CONFIG_TEST_CASE="+testCase)
	for k, v := range extraEnv {
		filtered = append(filtered, k+"="+v)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestFatalHelper", "-test.v=false")
	cmd.Env = filtered
	cmd.Stdout = nil
	cmd.Stderr = nil

	err := cmd.Run()
	if err == nil {
		t.Fatal("expected process to exit with error, but it succeeded")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("unexpected error type: %v", err)
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("expected exit code 1, got %d", exitErr.ExitCode())
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

	cleanup6 := setEnv(t, "JWT_SECRET", "supersecretkeywithatleast32chars")
	defer cleanup6()
	cleanup7 := setEnv(t, "SESSION_SECRET", "sessionsupersecretkey32charslong!!")
	defer cleanup7()
	cleanup8 := setEnv(t, "ADMIN_EMAIL", "admin@test.com")
	defer cleanup8()
	cleanup9 := setEnv(t, "ADMIN_PASSWORD", "securepassword12345")
	defer cleanup9()

	cfg := LoadConfig()

	assert.Equal(t, "localhost", cfg.Database.Host)
	assert.Equal(t, "5432", cfg.Database.Port)
	assert.Equal(t, "testuser", cfg.Database.User)
	assert.Equal(t, "testpass", cfg.Database.Password)
	assert.Equal(t, "testdb", cfg.Database.Name)
	assert.Equal(t, "disable", cfg.Database.SSLMode)
	assert.Equal(t, "supersecretkeywithatleast32chars", cfg.JWT.Secret)
	assert.Equal(t, "sessionsupersecretkey32charslong!!", cfg.Session.Secret)
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
	cleanup6 := setEnv(t, "JWT_SECRET", "supersecretkeywithatleast32chars")
	defer cleanup6()
	cleanup7 := setEnv(t, "SESSION_SECRET", "sessionsupersecretkey32charslong!!")
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

	cfg := LoadConfig()

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
	cleanup6 := setEnv(t, "JWT_SECRET", "supersecretkeywithatleast32chars")
	defer cleanup6()
	cleanup7 := setEnv(t, "SESSION_SECRET", "sessionsupersecretkey32charslong!!")
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

	cfg := LoadConfig()
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
	cleanup6 := setEnv(t, "JWT_SECRET", "supersecretkeywithatleast32chars")
	defer cleanup6()
	cleanup7 := setEnv(t, "SESSION_SECRET", "sessionsupersecretkey32charslong!!")
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

	cfg := LoadConfig()
	assert.True(t, cfg.SMTP.Enabled)
	assert.Equal(t, "smtp.test.com", cfg.SMTP.Host)
	assert.Equal(t, 587, cfg.SMTP.Port)
	assert.Equal(t, "user", cfg.SMTP.User)
	assert.Equal(t, "pass", cfg.SMTP.Password)
	assert.Equal(t, "from@test.com", cfg.SMTP.From)
}

func TestLoadConfig_StripeEnabled(t *testing.T) {
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
	cleanup6 := setEnv(t, "JWT_SECRET", "supersecretkeywithatleast32chars")
	defer cleanup6()
	cleanup7 := setEnv(t, "SESSION_SECRET", "sessionsupersecretkey32charslong!!")
	defer cleanup7()
	cleanup8 := setEnv(t, "ADMIN_EMAIL", "a@b.c")
	defer cleanup8()
	cleanup9 := setEnv(t, "ADMIN_PASSWORD", "securepassword12345")
	defer cleanup9()

	cleanup10 := setEnv(t, "STRIPE_ENABLED", "true")
	defer cleanup10()
	cleanup11 := setEnv(t, "STRIPE_SECRET_KEY", "sk_test_123")
	defer cleanup11()
	cleanup12 := setEnv(t, "STRIPE_WEBHOOK_SECRET", "wh_sec_123")
	defer cleanup12()

	cfg := LoadConfig()
	assert.True(t, cfg.Stripe.Enabled)
	assert.Equal(t, "sk_test_123", cfg.Stripe.SecretKey)
	assert.Equal(t, "wh_sec_123", cfg.Stripe.WebhookSecret)
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
	cleanup6 := setEnv(t, "JWT_SECRET", "supersecretkeywithatleast32chars")
	defer cleanup6()
	cleanup7 := setEnv(t, "SESSION_SECRET", "sessionsupersecretkey32charslong!!")
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

	cfg := LoadConfig()
	assert.True(t, cfg.ReCAPTCHA.Enabled)
	assert.Equal(t, "sitekey", cfg.ReCAPTCHA.SiteKey)
	assert.Equal(t, "secretkey", cfg.ReCAPTCHA.SecretKey)
}

// =============================================================================
// Тесты фатальных ошибок (через подпроцесс)
// =============================================================================

func TestLoadConfig_MissingRequired(t *testing.T) {
	runFatalSubtest(t, testCaseMissingRequired,
		[]string{"DB_HOST"},
		nil,
	)
}

func TestLoadConfig_JWTSecretTooShort(t *testing.T) {
	runFatalSubtest(t, testCaseJWTShort,
		nil,
		nil,
	)
}

func TestLoadConfig_JWTSecretWeak(t *testing.T) {
	runFatalSubtest(t, testCaseJWTWeak,
		nil,
		nil,
	)
}

func TestLoadConfig_OAuthEnabledMissingClientID(t *testing.T) {
	runFatalSubtest(t, testCaseOAuthMissing,
		nil,
		nil,
	)
}

func TestLoadConfig_SMTPEnabledMissingFrom(t *testing.T) {
	runFatalSubtest(t, testCaseSMTPMissingFrom,
		nil,
		nil,
	)
}

func TestLoadConfig_InvalidDuration(t *testing.T) {
	runFatalSubtest(t, testCaseInvalidDuration,
		nil,
		nil,
	)
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
	_ = os.Setenv("JWT_SECRET", "supersecretkeywithatleast32chars")
	_ = os.Setenv("SESSION_SECRET", "sessionsupersecretkey32charslong!!")
	_ = os.Setenv("ADMIN_EMAIL", "a@b.c")
	_ = os.Setenv("ADMIN_PASSWORD", "securepassword12345")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		LoadConfig()
	}
}
