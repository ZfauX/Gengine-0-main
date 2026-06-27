// Package config загружает и валидирует конфигурацию приложения из переменных окружения.
// Выполняет строгую проверку обязательных параметров, требует надёжные секреты и пароли,
// при обнаружении проблем завершает работу с логированием fatal-ошибки.
package config

import (
	"os"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"
)

// Config содержит все настройки приложения, сгруппированные по функциональным областям.
type Config struct {
	Server    ServerConfig    // настройки HTTP-сервера
	Database  DatabaseConfig  // параметры подключения к PostgreSQL
	Redis     RedisConfig     // опциональные настройки Redis
	JWT       JWTConfig       // параметры JWT-токенов
	Session   SessionConfig   // настройки сессий (подпись cookie)
	Admin     AdminConfig     // учётные данные администратора по умолчанию
	OAuth     OAuthConfig     // конфигурация OAuth-провайдеров
	Stripe    StripeConfig    // настройки Stripe (опционально)
	SMTP      SMTPConfig      // настройки SMTP-сервера (опционально)
	ReCAPTCHA ReCAPTCHAConfig // настройки reCAPTCHA (опционально)
	TLS       TLSConfig       // пути к TLS-сертификатам (опционально)
}

// ServerConfig содержит параметры HTTP-сервера.
type ServerConfig struct {
	Port       string // порт, на котором слушает сервер (по умолчанию 8080)
	GinMode    string // режим работы Gin (debug, release, test)
	BaseURL    string // базовый URL приложения для формирования ссылок
	MaxBackups int    // максимальное количество сохраняемых архивов логов (используется при ротации)
}

// DatabaseConfig содержит параметры подключения к PostgreSQL.
type DatabaseConfig struct {
	Host            string        // хост БД
	Port            string        // порт БД
	User            string        // имя пользователя
	Password        string        // пароль
	Name            string        // имя базы данных
	SSLMode         string        // режим SSL (disable, require, verify-full и т.д.)
	MaxOpenConns    int           // максимальное количество открытых соединений
	MaxIdleConns    int           // максимальное количество простаивающих соединений
	ConnMaxLifetime time.Duration // максимальное время жизни соединения
}

// RedisConfig содержит параметры подключения к Redis (опционально).
// Если Redis не используется, поля могут быть пустыми.
type RedisConfig struct {
	Host     string // хост Redis
	Port     string // порт Redis
	Password string // пароль (если требуется)
}

// JWTConfig содержит параметры JWT-аутентификации.
type JWTConfig struct {
	Secret        string        // секретный ключ для подписи токенов (минимум 32 символа)
	AccessExpiry  time.Duration // срок действия access-токена (по умолчанию 15 минут)
	RefreshExpiry time.Duration // срок действия refresh-токена (по умолчанию 7 дней)
}

// SessionConfig содержит параметры сессий.
type SessionConfig struct {
	Secret string // секретный ключ для подписи cookie сессии (минимум 32 символа)
}

// AdminConfig содержит учётные данные администратора, создаваемого при инициализации.
type AdminConfig struct {
	Email    string // email администратора
	Password string // пароль администратора (должен быть не менее 12 символов)
}

// OAuthConfig содержит конфигурацию OAuth-провайдеров.
type OAuthConfig struct {
	Google OAuthProvider // настройки Google OAuth
	GitHub OAuthProvider // настройки GitHub OAuth
	Yandex OAuthProvider // настройки Yandex OAuth
}

// OAuthProvider содержит параметры одного OAuth-провайдера.
type OAuthProvider struct {
	Enabled      bool   // включён ли провайдер
	ClientID     string // Client ID приложения
	ClientSecret string // Client Secret приложения
}

// StripeConfig содержит параметры Stripe (опционально).
type StripeConfig struct {
	Enabled       bool   // включена ли интеграция со Stripe
	SecretKey     string // секретный ключ Stripe API
	WebhookSecret string // секрет для проверки подписи вебхуков
}

// SMTPConfig содержит параметры SMTP-сервера (опционально).
type SMTPConfig struct {
	Enabled  bool   // включена ли отправка email
	Host     string // хост SMTP-сервера
	Port     int    // порт SMTP-сервера (обычно 587)
	User     string // имя пользователя для аутентификации
	Password string // пароль для аутентификации
	From     string // адрес отправителя (обязателен, если SMTP включён)
}

// ReCAPTCHAConfig содержит параметры reCAPTCHA (опционально).
type ReCAPTCHAConfig struct {
	Enabled   bool   // включена ли проверка reCAPTCHA
	SiteKey   string // публичный ключ для отображения виджета
	SecretKey string // секретный ключ для проверки ответа
}

// TLSConfig содержит пути к TLS-сертификатам (опционально).
// Если заполнены, сервер будет запущен с HTTPS.
type TLSConfig struct {
	CertFile string // путь к файлу сертификата (.crt или .pem)
	KeyFile  string // путь к файлу приватного ключа (.key)
}

// LoadConfig загружает конфигурацию из переменных окружения с жёсткой проверкой обязательных секретов.
// Выполняет следующие проверки:
// - наличие всех обязательных переменных (DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME, ADMIN_EMAIL, ADMIN_PASSWORD);
// - длина и надёжность JWT_SECRET и SESSION_SECRET (минимум 32 символа, не содержат слабых значений);
// - сложность ADMIN_PASSWORD (минимум 12 символов);
// - корректность формата длительностей;
// - если OAuth, Stripe, SMTP или reCAPTCHA включены, проверяет наличие необходимых ключей.
// При обнаружении ошибок завершает работу приложения с fatal-логированием.
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

// =============================================================================
// Вспомогательные функции (не экспортируются)
// =============================================================================

// getEnvOrDefault возвращает значение переменной окружения или fallback, если переменная не установлена.
func getEnvOrDefault(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// requireEnv требует наличия переменной окружения, иначе завершает работу с fatal-логированием.
func requireEnv(key string) string {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		log.Fatal().Str("variable", key).Msg("required environment variable is not set")
	}
	return value
}

// requireStrongSecret проверяет, что переменная окружения установлена, имеет длину не менее minLen
// и не содержит типичных слабых значений. При нарушении условий завершает работу.
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

// requireStrongPassword проверяет, что пароль администратора имеет длину не менее minLen.
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

// parseDuration преобразует строку в time.Duration, используя значение по умолчанию при отсутствии переменной.
// При ошибке парсинга завершает работу.
func parseDuration(key, defaultVal string) time.Duration {
	val := getEnvOrDefault(key, defaultVal)
	d, err := time.ParseDuration(val)
	if err != nil {
		log.Fatal().Str("variable", key).Err(err).Msg("invalid duration")
	}
	return d
}

// loadOAuthProvider загружает настройки OAuth-провайдера по префиксу.
// Если провайдер включён, требует наличия CLIENT_ID и CLIENT_SECRET.
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

// loadStripeConfig загружает настройки Stripe, если они включены.
// При включении требует наличия STRIPE_SECRET_KEY.
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

// loadSMTPConfig загружает настройки SMTP, если они включены.
// При включении требует наличия SMTP_HOST и SMTP_FROM.
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

// loadReCAPTCHAConfig загружает настройки reCAPTCHA, если они включены.
// При включении требует наличия RECAPTCHA_SITE_KEY и RECAPTCHA_SECRET_KEY.
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

// getEnvAsInt возвращает значение переменной окружения как целое число или fallback при ошибке.
func getEnvAsInt(key string, fallback int) int {
	if value, ok := os.LookupEnv(key); ok {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
		log.Warn().Str("variable", key).Int("default", fallback).Msg("invalid integer, using default")
	}
	return fallback
}
