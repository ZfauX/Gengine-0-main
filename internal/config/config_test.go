// internal/config/config_test.go
package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain СѓСЃС‚Р°РЅР°РІР»РёРІР°РµС‚ РѕР±СЏР·Р°С‚РµР»СЊРЅС‹Рµ РїРµСЂРµРјРµРЅРЅС‹Рµ РѕРєСЂСѓР¶РµРЅРёСЏ РїРµСЂРµРґ Р·Р°РїСѓСЃРєРѕРј Р»СЋР±РѕРіРѕ С‚РµСЃС‚Р°,
// С‡С‚РѕР±С‹ РёР·Р±РµР¶Р°С‚СЊ РѕС€РёР±РѕРє РїСЂРё РІС‹Р·РѕРІРµ LoadConfig РІ С‚РµСЃС‚Р°С….
func TestMain(m *testing.M) {
	_ = os.Setenv("DB_HOST", "localhost")
	_ = os.Setenv("DB_PORT", "5432")
	_ = os.Setenv("DB_USER", "test")
	_ = os.Setenv("DB_PASSWORD", "test")
	_ = os.Setenv("DB_NAME", "test")
	_ = os.Setenv("JWT_SECRET", "xK9mP2vL5nQ8wR3tY6uI0oP4sD7fG1hJ")
	_ = os.Setenv("SESSION_SECRET", "aB3cD4eF5gH6iJ7kL8mN9oP0qR1sT2uV3wX4yZ")
	_ = os.Setenv("ADMIN_EMAIL", "admin@test.com")
	_ = os.Setenv("ADMIN_PASSWORD", "SecurePass12345!")

	code := m.Run()
	os.Exit(code)
}

// =============================================================================
// Р’СЃРїРѕРјРѕРіР°С‚РµР»СЊРЅС‹Рµ С„СѓРЅРєС†РёРё РґР»СЏ С‚РµСЃС‚РѕРІ
// =============================================================================

// setEnv СѓСЃС‚Р°РЅР°РІР»РёРІР°РµС‚ РїРµСЂРµРјРµРЅРЅСѓСЋ РѕРєСЂСѓР¶РµРЅРёСЏ Рё РІРѕР·РІСЂР°С‰Р°РµС‚ С„СѓРЅРєС†РёСЋ РґР»СЏ РµС‘ РІРѕСЃСЃС‚Р°РЅРѕРІР»РµРЅРёСЏ.
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
// РўРµСЃС‚С‹ РґР»СЏ LoadConfig (СѓСЃРїРµС€РЅС‹Рµ СЃС†РµРЅР°СЂРёРё)
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
	cleanup9 := setEnv(t, "ADMIN_PASSWORD", "SecurePass12345!")
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
	assert.Equal(t, "SecurePass12345!", cfg.Admin.Password)
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
	cleanup9 := setEnv(t, "ADMIN_PASSWORD", "SecurePass12345!")
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
	cleanup9 := setEnv(t, "ADMIN_PASSWORD", "SecurePass12345!")
	defer cleanup9()

	cleanup10 := setEnv(t, "VK_ENABLED", "true")
	defer cleanup10()
	cleanup11 := setEnv(t, "VK_CLIENT_ID", "vk_id")
	defer cleanup11()
	cleanup12 := setEnv(t, "VK_CLIENT_SECRET", "vk_secret")
	defer cleanup12()

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.True(t, cfg.OAuth.VK.Enabled)
	assert.Equal(t, "vk_id", cfg.OAuth.VK.ClientID)
	assert.Equal(t, "vk_secret", cfg.OAuth.VK.ClientSecret)
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
	cleanup9 := setEnv(t, "ADMIN_PASSWORD", "SecurePass12345!")
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
	cleanup9 := setEnv(t, "ADMIN_PASSWORD", "SecurePass12345!")
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
// РўРµСЃС‚С‹ РѕС€РёР±РѕС‡РЅС‹С… СЃРёС‚СѓР°С†РёР№ (РїСЂСЏРјР°СЏ РїСЂРѕРІРµСЂРєР° РѕС€РёР±РѕРє)
// =============================================================================

func TestLoadConfig_MissingRequired(t *testing.T) {
	// РЈРґР°Р»СЏРµРј РѕР±СЏР·Р°С‚РµР»СЊРЅСѓСЋ РїРµСЂРµРјРµРЅРЅСѓСЋ Рё РїСЂРѕРІРµСЂСЏРµРј РѕС€РёР±РєСѓ
	cleanup := setEnv(t, "DB_HOST", "") // РїСѓСЃС‚РѕРµ Р·РЅР°С‡РµРЅРёРµ
	defer cleanup()
	// РўР°РєР¶Рµ РЅСѓР¶РЅРѕ СѓР±РµРґРёС‚СЊСЃСЏ, С‡С‚Рѕ РґСЂСѓРіРёРµ РїРµСЂРµРјРµРЅРЅС‹Рµ СѓСЃС‚Р°РЅРѕРІР»РµРЅС‹ (СѓР¶Рµ РµСЃС‚СЊ РІ TestMain)
	// РќРѕ DB_HOST РґРѕР»Р¶РµРЅ Р±С‹С‚СЊ РїСѓСЃС‚С‹Рј
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

func TestLoadConfig_JWTSecretPrefixNotWeak(t *testing.T) {
	// Regression: "change-me-..." should NOT be rejected as weak with exact-match-only check
	cleanup := setEnv(t, "JWT_SECRET", "change-me-12345678901234567890")
	defer cleanup()
	_, err := LoadConfig()
	// It should fail on length (< 32), not on weak value
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "weak/default value")
	assert.Contains(t, err.Error(), "at least 32 characters")
}

func TestLoadConfig_OAuthEnabledMissingClientID(t *testing.T) {
	cleanup1 := setEnv(t, "VK_ENABLED", "true")
	defer cleanup1()
	cleanup2 := setEnv(t, "VK_CLIENT_ID", "") // РїСѓСЃС‚Рѕ
	defer cleanup2()
	cleanup3 := setEnv(t, "VK_CLIENT_SECRET", "") // РїСѓСЃС‚Рѕ
	defer cleanup3()
	_, err := LoadConfig()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "VK")
}

func TestLoadConfig_SMTPEnabledMissingFrom(t *testing.T) {
	cleanup1 := setEnv(t, "SMTP_ENABLED", "true")
	defer cleanup1()
	cleanup2 := setEnv(t, "SMTP_HOST", "smtp.example.com")
	defer cleanup2()
	cleanup3 := setEnv(t, "SMTP_FROM", "") // РїСѓСЃС‚Рѕ
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
// Р‘РµРЅС‡РјР°СЂРє РґР»СЏ LoadConfig
// =============================================================================

func BenchmarkLoadConfig(b *testing.B) {
	require.NoError(b, os.Setenv("DB_HOST", "localhost"))
	require.NoError(b, os.Setenv("DB_PORT", "5432"))
	require.NoError(b, os.Setenv("DB_USER", "user"))
	require.NoError(b, os.Setenv("DB_PASSWORD", "pass"))
	require.NoError(b, os.Setenv("DB_NAME", "db"))
	require.NoError(b, os.Setenv("JWT_SECRET", "xK9mP2vL5nQ8wR3tY6uI0oP4sD7fG1hJ"))
	require.NoError(b, os.Setenv("SESSION_SECRET", "aB3cD4eF5gH6iJ7kL8mN9oP0qR1sT2uV3wX4yZ"))
	require.NoError(b, os.Setenv("ADMIN_EMAIL", "a@b.c"))
	require.NoError(b, os.Setenv("ADMIN_PASSWORD", "SecurePass12345!"))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = LoadConfig()
	}
}
