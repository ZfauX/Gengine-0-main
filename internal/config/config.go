// Package config загружает и валидирует конфигурацию приложения из переменных окружения.
// Выполняет строгую проверку обязательных параметров, требует надёжные секреты и пароли,
// при обнаружении проблем возвращает ошибку.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultSMTPPort        = 587
	defaultMaxBackups      = 10
	defaultLogMaxSizeMB    = 100
	defaultLogMaxAgeDays   = 28
	defaultDBMaxOpenConns  = 50
	defaultDBMaxIdleConns  = 25
	defaultWSMaxTotalConns = 1000
	defaultWSMaxConnsPerIP = 50
)

// Config содержит все настройки приложения, сгруппированные по функциональным областям.
type Config struct {
	Server    ServerConfig    // настройки HTTP-сервера и логирования
	Database  DatabaseConfig  // параметры подключения к PostgreSQL
	Valkey    ValkeyConfig    // параметры подключения к Valkey (Redis-compatible)
	JWT       JWTConfig       // параметры JWT-токенов
	Session   SessionConfig   // настройки сессий (подпись cookie)
	Admin     AdminConfig     // учётные данные администратора по умолчанию
	OAuth     OAuthConfig     // конфигурация OAuth-провайдеров
	SMTP      SMTPConfig      // настройки SMTP-сервера (опционально)
	ReCAPTCHA ReCAPTCHAConfig // настройки reCAPTCHA (опционально)
	TLS       TLSConfig       // пути к TLS-сертификатам (опционально)
	Sentry    SentryConfig    // настройки Sentry (опционально)
	WebSocket WebSocketConfig // настройки WebSocket-соединений
}

// ServerConfig содержит параметры HTTP-сервера и логирования.
type ServerConfig struct {
	Port           string // порт, на котором слушает сервер (по умолчанию 8080)
	GinMode        string // режим работы Gin (debug, release, test)
	BaseURL        string // базовый URL приложения для формирования ссылок
	MaxBackups     int    // максимальное количество сохраняемых архивов логов
	LogFilePath    string // путь к файлу логов (по умолчанию "logs/app.log")
	LogMaxSize     int    // максимальный размер файла лога в МБ (по умолчанию 100)
	LogMaxAge      int    // максимальное количество дней хранения логов (по умолчанию 28)
	LogCompress    bool   // сжимать ли архивы (по умолчанию true)
	LogFormat      string // формат вывода логов: "console" или "json" (по умолчанию "console")
	StaticDir      string // путь к статическим файлам (по умолчанию "static")
	UploadsDir     string // путь к загружаемым файлам (по умолчанию "uploads")
	TrustedProxies string // доверенные прокси через запятую (например: 127.0.0.1,192.168.0.0/24)
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
	ConnMaxIdleTime time.Duration // максимальное время простоя соединения
}

// ValkeyConfig содержит параметры подключения к Valkey (опционально).
// Valkey полностью совместим с Redis API.
// Если Valkey не используется, поля могут быть пустыми.
type ValkeyConfig struct {
	Host     string // хост Valkey
	Port     string // порт Valkey
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

// SentryConfig содержит параметры Sentry (опционально).
type SentryConfig struct {
	Enabled     bool    // включена ли интеграция с Sentry
	DSN         string  // DSN для подключения к Sentry
	TracingRate float64 // доля трассировки (0.0-1.0)
}

// WebSocketConfig содержит параметры WebSocket-соединений.
type WebSocketConfig struct {
	MaxTotalConns int // максимальное общее количество соединений (0 = без ограничения)
	MaxConnsPerIP int // максимальное количество соединений с одного IP (0 = без ограничения)
}

// LoadConfig загружает конфигурацию из переменных окружения с жёсткой проверкой обязательных секретов.
// Выполняет проверки и возвращает конфигурацию или ошибку.
func LoadConfig() (*Config, error) {
	cfg := &Config{}

	// Сервер
	cfg.Server.Port = getEnvOrDefault("PORT", "8080")
	cfg.Server.GinMode = getEnvOrDefault("GIN_MODE", "debug")
	cfg.Server.BaseURL = getEnvOrDefault("BASE_URL", "http://localhost:"+cfg.Server.Port)
	cfg.Server.MaxBackups = getEnvAsInt("LOG_MAX_BACKUPS", defaultMaxBackups)
	cfg.Server.LogFilePath = getEnvOrDefault("LOG_FILE_PATH", "logs/app.log")
	cfg.Server.LogMaxSize = getEnvAsInt("LOG_MAX_SIZE", defaultLogMaxSizeMB)
	cfg.Server.LogMaxAge = getEnvAsInt("LOG_MAX_AGE", defaultLogMaxAgeDays)
	cfg.Server.LogCompress = getEnvAsBool("LOG_COMPRESS", true)
	cfg.Server.LogFormat = getEnvOrDefault("LOG_FORMAT", "console") // console или json
	cfg.Server.StaticDir = getEnvOrDefault("STATIC_DIR", "static")
	cfg.Server.UploadsDir = getEnvOrDefault("UPLOADS_DIR", "uploads")

	// База данных (обязательные параметры)
	var err error
	if cfg.Database.Host, err = requireEnv("DB_HOST"); err != nil {
		return nil, err
	}
	if cfg.Database.Port, err = requireEnv("DB_PORT"); err != nil {
		return nil, err
	}
	if cfg.Database.User, err = requireEnv("DB_USER"); err != nil {
		return nil, err
	}
	if cfg.Database.Password, err = requireEnv("DB_PASSWORD"); err != nil {
		return nil, err
	}
	if cfg.Database.Name, err = requireEnv("DB_NAME"); err != nil {
		return nil, err
	}
	cfg.Database.SSLMode = getEnvOrDefault("DB_SSLMODE", "disable")
	cfg.Database.MaxOpenConns = getEnvAsInt("DB_MAX_OPEN_CONNS", defaultDBMaxOpenConns)
	cfg.Database.MaxIdleConns = getEnvAsInt("DB_MAX_IDLE_CONNS", defaultDBMaxIdleConns)
	if cfg.Database.ConnMaxLifetime, err = parseDuration("DB_CONN_MAX_LIFETIME", "30m"); err != nil {
		return nil, err
	}
	if cfg.Database.ConnMaxIdleTime, err = parseDuration("DB_CONN_MAX_IDLE_TIME", "10m"); err != nil {
		return nil, err
	}

	// Valkey (опционально)
	cfg.Valkey.Host = os.Getenv("VALKEY_HOST")
	cfg.Valkey.Port = os.Getenv("VALKEY_PORT")
	cfg.Valkey.Password = os.Getenv("VALKEY_PASSWORD")

	// JWT – критично, без дефолтов
	if cfg.JWT.Secret, err = requireStrongSecret("JWT_SECRET", 32); err != nil {
		return nil, err
	}
	if cfg.JWT.AccessExpiry, err = parseDuration("JWT_ACCESS_EXPIRY", "15m"); err != nil {
		return nil, err
	}
	if cfg.JWT.RefreshExpiry, err = parseDuration("JWT_REFRESH_EXPIRY", "168h"); err != nil {
		return nil, err
	}

	// Сессионный ключ – критично
	if cfg.Session.Secret, err = requireStrongSecret("SESSION_SECRET", 32); err != nil {
		return nil, err
	}

	// Администратор – обязателен
	if cfg.Admin.Email, err = requireEnv("ADMIN_EMAIL"); err != nil {
		return nil, err
	}
	if cfg.Admin.Password, err = requireStrongPassword("ADMIN_PASSWORD", 12); err != nil {
		return nil, err
	}

	// OAuth провайдеры – каждый со своим флагом включения
	if cfg.OAuth.Google, err = loadOAuthProvider("GOOGLE"); err != nil {
		return nil, err
	}
	if cfg.OAuth.GitHub, err = loadOAuthProvider("GITHUB"); err != nil {
		return nil, err
	}
	if cfg.OAuth.Yandex, err = loadOAuthProvider("YANDEX"); err != nil {
		return nil, err
	}

	// SMTP
	if cfg.SMTP, err = loadSMTPConfig(); err != nil {
		return nil, err
	}

	// reCAPTCHA
	if cfg.ReCAPTCHA, err = loadReCAPTCHAConfig(); err != nil {
		return nil, err
	}

	// TLS
	cfg.TLS.CertFile = os.Getenv("TLS_CERT_FILE")
	cfg.TLS.KeyFile = os.Getenv("TLS_KEY_FILE")

	// Sentry
	if cfg.Sentry, err = loadSentryConfig(); err != nil {
		return nil, err
	}

	// WebSocket
	cfg.WebSocket.MaxTotalConns = getEnvAsInt("WS_MAX_TOTAL_CONNS", defaultWSMaxTotalConns)
	cfg.WebSocket.MaxConnsPerIP = getEnvAsInt("WS_MAX_CONNS_PER_IP", defaultWSMaxConnsPerIP)

	return cfg, nil
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

// requireEnv требует наличия переменной окружения, иначе возвращает ошибку.
func requireEnv(key string) (string, error) {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return "", fmt.Errorf("required environment variable %s is not set", key)
	}
	return value, nil
}

// requireStrongSecret проверяет, что переменная окружения установлена, имеет длину не менее minLen
// и не содержит типичных слабых значений. При нарушении условий возвращает ошибку.
func requireStrongSecret(key string, minLen int) (string, error) {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return "", fmt.Errorf("environment variable %s must be set to a strong random string", key)
	}
	if len(value) < minLen {
		return "", fmt.Errorf("environment variable %s must be at least %d characters long (current: %d)", key, minLen, len(value))
	}
	commonWeak := []string{"change-me", "secret", "password", "admin", "123456", "your-secret"}
	lowerValue := strings.ToLower(value)
	for _, w := range commonWeak {
		if strings.Contains(lowerValue, w) {
			return "", fmt.Errorf("environment variable %s appears to be a weak/default value, please change it", key)
		}
	}
	return value, nil
}

// requireStrongPassword проверяет, что пароль администратора имеет длину не менее minLen.
func requireStrongPassword(key string, minLen int) (string, error) {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return "", fmt.Errorf("environment variable %s is required (admin password)", key)
	}
	if len(value) < minLen {
		return "", fmt.Errorf("environment variable %s must be at least %d characters long (current: %d)", key, minLen, len(value))
	}
	return value, nil
}

// parseDuration преобразует строку в time.Duration, используя значение по умолчанию при отсутствии переменной.
// При ошибке парсинга возвращает ошибку.
func parseDuration(key, defaultVal string) (time.Duration, error) {
	val := getEnvOrDefault(key, defaultVal)
	d, err := time.ParseDuration(val)
	if err != nil {
		return 0, fmt.Errorf("invalid duration for %s: %w", key, err)
	}
	return d, nil
}

// loadOAuthProvider загружает настройки OAuth-провайдера по префиксу.
// Если провайдер включён, требует наличия CLIENT_ID и CLIENT_SECRET.
func loadOAuthProvider(prefix string) (OAuthProvider, error) {
	enabledEnv := prefix + "_ENABLED"
	enabled, _ := strconv.ParseBool(os.Getenv(enabledEnv))
	if !enabled {
		return OAuthProvider{Enabled: false}, nil
	}
	clientID := os.Getenv(prefix + "_CLIENT_ID")
	clientSecret := os.Getenv(prefix + "_CLIENT_SECRET")
	if clientID == "" || clientSecret == "" {
		return OAuthProvider{}, fmt.Errorf("OAuth provider %s is enabled but CLIENT_ID or CLIENT_SECRET is missing", prefix)
	}
	return OAuthProvider{
		Enabled:      true,
		ClientID:     clientID,
		ClientSecret: clientSecret,
	}, nil
}

// loadSMTPConfig загружает настройки SMTP, если они включены.
// При включении требует наличия SMTP_HOST и SMTP_FROM.
func loadSMTPConfig() (SMTPConfig, error) {
	enabled, _ := strconv.ParseBool(os.Getenv("SMTP_ENABLED"))
	if !enabled {
		return SMTPConfig{Enabled: false}, nil
	}
	host := os.Getenv("SMTP_HOST")
	if host == "" {
		return SMTPConfig{}, errors.New("SMTP_ENABLED is true but SMTP_HOST is missing")
	}
	portStr := getEnvOrDefault("SMTP_PORT", strconv.Itoa(defaultSMTPPort))
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return SMTPConfig{}, fmt.Errorf("invalid SMTP_PORT: %w", err)
	}
	user := os.Getenv("SMTP_USER")
	password := os.Getenv("SMTP_PASSWORD")
	from := os.Getenv("SMTP_FROM")
	if from == "" {
		return SMTPConfig{}, errors.New("SMTP_ENABLED is true but SMTP_FROM is missing")
	}
	return SMTPConfig{
		Enabled:  true,
		Host:     host,
		Port:     port,
		User:     user,
		Password: password,
		From:     from,
	}, nil
}

// loadReCAPTCHAConfig загружает настройки reCAPTCHA, если они включены.
// При включении требует наличия RECAPTCHA_SITE_KEY и RECAPTCHA_SECRET_KEY.
func loadReCAPTCHAConfig() (ReCAPTCHAConfig, error) {
	enabled, _ := strconv.ParseBool(os.Getenv("RECAPTCHA_ENABLED"))
	if !enabled {
		return ReCAPTCHAConfig{Enabled: false}, nil
	}
	siteKey := os.Getenv("RECAPTCHA_SITE_KEY")
	secretKey := os.Getenv("RECAPTCHA_SECRET_KEY")
	if siteKey == "" || secretKey == "" {
		return ReCAPTCHAConfig{}, errors.New("RECAPTCHA_ENABLED is true but RECAPTCHA_SITE_KEY or RECAPTCHA_SECRET_KEY is missing")
	}
	return ReCAPTCHAConfig{
		Enabled:   true,
		SiteKey:   siteKey,
		SecretKey: secretKey,
	}, nil
}

// loadSentryConfig загружает настройки Sentry, если они включены.
// При включении требует наличия SENTRY_DSN.
func loadSentryConfig() (SentryConfig, error) {
	enabled, _ := strconv.ParseBool(os.Getenv("SENTRY_ENABLED"))
	if !enabled {
		return SentryConfig{Enabled: false}, nil
	}
	dsn := os.Getenv("SENTRY_DSN")
	if dsn == "" {
		return SentryConfig{}, errors.New("SENTRY_ENABLED is true but SENTRY_DSN is missing")
	}
	tracingRate := getEnvAsFloat("SENTRY_TRACING_RATE", 0.1)
	return SentryConfig{
		Enabled:     true,
		DSN:         dsn,
		TracingRate: tracingRate,
	}, nil
}

// getEnvAsInt возвращает значение переменной окружения как целое число или fallback при ошибке.
func getEnvAsInt(key string, fallback int) int {
	if value, ok := os.LookupEnv(key); ok {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return fallback
}

// getEnvAsBool возвращает значение переменной окружения как булево или fallback при ошибке.
func getEnvAsBool(key string, fallback bool) bool {
	if value, ok := os.LookupEnv(key); ok {
		if boolValue, err := strconv.ParseBool(value); err == nil {
			return boolValue
		}
	}
	return fallback
}

// getEnvAsFloat возвращает значение переменной окружения как float64 или fallback при ошибке.
func getEnvAsFloat(key string, fallback float64) float64 {
	if value, ok := os.LookupEnv(key); ok {
		if floatValue, err := strconv.ParseFloat(value, 64); err == nil {
			return floatValue
		}
	}
	return fallback
}
