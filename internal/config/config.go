// Package config загружает и валидирует конфигурацию приложения из переменных окружения.
// Package config загружает и валидирует конфигурацию приложения из переменных окружения.
package config

import (
	"os"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"
)

// Config содержит все настройки приложения.
type Config struct {
	Server   ServerConfig
	Database DatabaseConfig
	Redis    RedisConfig
	JWT      JWTConfig
	Session  SessionConfig
	Admin    AdminConfig
	OAuth    OAuthConfig
	Stripe   StripeConfig
	SMTP     SMTPConfig
	ReCAPTCHA ReCAPTCHAConfig
	TLS      TLSConfig
}

// ServerConfig содержит параметры HTTP-сервера.
type ServerConfig struct {
	Port       string
	GinMode    string
	BaseURL    string
	MaxBackups int
}

// DatabaseConfig содержит параметры подключения к PostgreSQL.
type DatabaseConfig struct {
	Host            string
	Port            string
	User            string
	Password        string
	Name            string
	SSLMode         string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// RedisConfig содержит параметры подключения к Redis (опционально).
type RedisConfig struct {
	Host     string
	Port     string
	Password string
}

// JWTConfig содержит параметры JWT-аутентификации.
type JWTConfig struct {
	Secret        string
	AccessExpiry  time.Duration
	RefreshExpiry time.Duration
}

// SessionConfig содержит параметры сессий.
type SessionConfig struct {
	Secret string
}

// AdminConfig содержит учётные данные администратора.
type AdminConfig struct {
	Email    string
	Password string
}

// OAuthConfig содержит конфигурацию OAuth-провайдеров.
type OAuthConfig struct {
	Google OAuthProvider
	GitHub OAuthProvider
	Yandex OAuthProvider
}

// OAuthProvider содержит параметры одного OAuth-провайдера.
type OAuthProvider struct {
	Enabled      bool
	ClientID     string
	ClientSecret string
}

// StripeConfig содержит параметры Stripe.
type StripeConfig struct {
	Enabled       bool
	SecretKey     string
	WebhookSecret string
}

// SMTPConfig содержит параметры SMTP-сервера.
type SMTPConfig struct {
	Enabled  bool
	Host     string
	Port     int
	User     string
	Password string
	From     string
}

// ReCAPTCHAConfig содержит параметры reCAPTCHA.
type ReCAPTCHAConfig struct {
	Enabled   bool
	SiteKey   string
	SecretKey string
}

// TLSConfig содержит пути к TLS-сертификатам.
type TLSConfig struct {
	CertFile string
	KeyFile  string
}

// LoadConfig загружает конфигурацию из переменных окружения с жёсткой проверкой обязательных секретов.
func LoadConfig() *Config {
	cfg := &Config{}

	// Сервер
	cfg.Server.Port = getEnvOrDefault("PORT", "8080")
	cfg.Server.GinMode = getEnvOrDefault("GIN_MODE", "debug")
	cfg.Server.BaseURL = getEnvOrDefault("BASE_URL", "http://localhost:"+cfg.Server.Port)
	cfg.Server.MaxBackups = getEnvAsInt("BACKUP_MAX_BACKUPS", 10)

	// База данных (обязательные параметры)
	cfg.Database.Host = requireEnv("DB_HOST")
	cfg.Database.Port = requireEnv("DB_PORT")
	cfg.Database.User = requireEnv("DB_USER")
	cfg.Database.Password = requireEnv("DB_PASSWORD")
	cfg.Database.Name = requireEnv("DB_NAME")
	cfg.Database.SSLMode = getEnvOrDefault("DB_SSLMODE", "disable")
	cfg.Database.MaxOpenConns = getEnvAsInt("DB_MAX_OPEN_CONNS", 25)
	cfg.Database.MaxIdleConns = getEnvAsInt("DB_MAX_IDLE_CONNS", 10)
	cfg.Database.ConnMaxLifetime = parseDuration("DB_CONN_MAX_LIFETIME", "5m")

	// Redis (опционально)
	cfg.Redis.Host = os.Getenv("REDIS_HOST")
	cfg.Redis.Port = os.Getenv("REDIS_PORT")
	cfg.Redis.Password = os.Getenv("REDIS_PASSWORD")

	// JWT – критично, без дефолтов
	cfg.JWT.Secret = requireStrongSecret("JWT_SECRET", 32)
	cfg.JWT.AccessExpiry = parseDuration("JWT_ACCESS_EXPIRY", "15m")
	cfg.JWT.RefreshExpiry = parseDuration("JWT_REFRESH_EXPIRY", "168h")

	// Сессионный ключ – критично
	cfg.Session.Secret = requireStrongSecret("SESSION_SECRET", 32)

	// Администратор – обязателен
	cfg.Admin.Email = requireEnv("ADMIN_EMAIL")
	cfg.Admin.Password = requireStrongPassword("ADMIN_PASSWORD", 12)

	// OAuth провайдеры – каждый со своим флагом включения
	cfg.OAuth.Google = loadOAuthProvider("GOOGLE")
	cfg.OAuth.GitHub = loadOAuthProvider("GITHUB")
	cfg.OAuth.Yandex = loadOAuthProvider("YANDEX")

	// Stripe
	cfg.Stripe = loadStripeConfig()

	// SMTP
	cfg.SMTP = loadSMTPConfig()

	// reCAPTCHA
	cfg.ReCAPTCHA = loadReCAPTCHAConfig()

	// TLS
	cfg.TLS.CertFile = os.Getenv("TLS_CERT_FILE")
	cfg.TLS.KeyFile = os.Getenv("TLS_KEY_FILE")

	return cfg
}

// Вспомогательные функции

func getEnvOrDefault(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func requireEnv(key string) string {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		log.Fatal().Str("variable", key).Msg("required environment variable is not set")
	}
	return value
}

func requireStrongSecret(key string, minLen int) string {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		log.Fatal().Str("variable", key).Msg("must be set to a strong random string")
	}
	if len(value) < minLen {
		log.Fatal().Str("variable", key).Int("min_length", minLen).Int("actual_length", len(value)).Msg("too short")
	}
	commonWeak := []string{"change-me", "secret", "password", "admin", "123456", "your-"}
	for _, w := range commonWeak {
		if len(value) >= len(w) && value[:len(w)] == w {
			log.Fatal().Str("variable", key).Msg("appears to be a weak/default value, please change it")
		}
	}
	return value
}

func requireStrongPassword(key string, minLen int) string {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		log.Fatal().Str("variable", key).Msg("is required (admin password)")
	}
	if len(value) < minLen {
		log.Fatal().Str("variable", key).Int("min_length", minLen).Int("actual_length", len(value)).Msg("must be at least min_length characters long")
	}
	return value
}

func parseDuration(key, defaultVal string) time.Duration {
	val := getEnvOrDefault(key, defaultVal)
	d, err := time.ParseDuration(val)
	if err != nil {
		log.Fatal().Str("variable", key).Err(err).Msg("invalid duration")
	}
	return d
}

func loadOAuthProvider(prefix string) OAuthProvider {
	enabledEnv := prefix + "_ENABLED"
	enabled, _ := strconv.ParseBool(os.Getenv(enabledEnv))
	if !enabled {
		return OAuthProvider{Enabled: false}
	}
	clientID := os.Getenv(prefix + "_CLIENT_ID")
	clientSecret := os.Getenv(prefix + "_CLIENT_SECRET")
	if clientID == "" || clientSecret == "" {
		log.Fatal().Str("provider", prefix).Msg("OAuth is enabled but CLIENT_ID or CLIENT_SECRET is missing")
	}
	return OAuthProvider{
		Enabled:      true,
		ClientID:     clientID,
		ClientSecret: clientSecret,
	}
}

func loadStripeConfig() StripeConfig {
	enabled, _ := strconv.ParseBool(os.Getenv("STRIPE_ENABLED"))
	if !enabled {
		return StripeConfig{Enabled: false}
	}
	secretKey := os.Getenv("STRIPE_SECRET_KEY")
	if secretKey == "" {
		log.Fatal().Msg("STRIPE_ENABLED is true but STRIPE_SECRET_KEY is not set")
	}
	webhookSecret := os.Getenv("STRIPE_WEBHOOK_SECRET")
	return StripeConfig{
		Enabled:       true,
		SecretKey:     secretKey,
		WebhookSecret: webhookSecret,
	}
}

func loadSMTPConfig() SMTPConfig {
	enabled, _ := strconv.ParseBool(os.Getenv("SMTP_ENABLED"))
	if !enabled {
		return SMTPConfig{Enabled: false}
	}
	host := os.Getenv("SMTP_HOST")
	if host == "" {
		log.Fatal().Msg("SMTP_ENABLED is true but SMTP_HOST is missing")
	}
	portStr := getEnvOrDefault("SMTP_PORT", "587")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid SMTP_PORT")
	}
	user := os.Getenv("SMTP_USER")
	password := os.Getenv("SMTP_PASSWORD")
	from := os.Getenv("SMTP_FROM")
	if from == "" {
		log.Fatal().Msg("SMTP_ENABLED is true but SMTP_FROM is missing")
	}
	return SMTPConfig{
		Enabled:  true,
		Host:     host,
		Port:     port,
		User:     user,
		Password: password,
		From:     from,
	}
}

func loadReCAPTCHAConfig() ReCAPTCHAConfig {
	enabled, _ := strconv.ParseBool(os.Getenv("RECAPTCHA_ENABLED"))
	if !enabled {
		return ReCAPTCHAConfig{Enabled: false}
	}
	siteKey := os.Getenv("RECAPTCHA_SITE_KEY")
	secretKey := os.Getenv("RECAPTCHA_SECRET_KEY")
	if siteKey == "" || secretKey == "" {
		log.Fatal().Msg("RECAPTCHA_ENABLED is true but RECAPTCHA_SITE_KEY or RECAPTCHA_SECRET_KEY is missing")
	}
	return ReCAPTCHAConfig{
		Enabled:   true,
		SiteKey:   siteKey,
		SecretKey: secretKey,
	}
}

func getEnvAsInt(key string, fallback int) int {
	if value, ok := os.LookupEnv(key); ok {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
		log.Warn().Str("variable", key).Int("default", fallback).Msg("invalid integer, using default")
	}
	return fallback
}