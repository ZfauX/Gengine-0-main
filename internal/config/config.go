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

	"github.com/SherClockHolmes/webpush-go"
	"github.com/rs/zerolog/log"
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
	VAPID     VAPIDConfig     // VAPID-ключи для Web Push (автогенерация при отсутствии)
	WebSocket WebSocketConfig // настройки WebSocket-соединений
}

// ServerConfig содержит параметры HTTP-сервера и логирования.
type ServerConfig struct {
	Port              string // порт, на котором слушает сервер (по умолчанию 8080)
	GinMode           string // режим работы Gin (debug, release, test)
	BaseURL           string // базовый URL приложения для формирования ссылок
	MaxBackups        int    // максимальное количество сохраняемых архивов логов
	LogFilePath       string // путь к файлу логов (по умолчанию "logs/app.log")
	LogMaxSize        int    // максимальный размер файла лога в МБ (по умолчанию 100)
	LogMaxAge         int    // максимальное количество дней хранения логов (по умолчанию 28)
	LogCompress       bool   // сжимать ли архивы (по умолчанию true)
	LogFormat         string // формат вывода логов: "console" или "json" (по умолчанию "console")
	StaticDir         string // путь к статическим файлам (по умолчанию "static")
	UploadsDir        string // путь к загружаемым файлам (по умолчанию "uploads")
	MaxUploadSize     int    // максимальный размер загружаемого файла в байтах (по умолчанию 5MB)
	MaxBodySize       int    // максимальный размер тела запроса в байтах (по умолчанию 10MB)
	TrustedProxies    string // доверенные прокси через запятую (например: 127.0.0.1,192.168.0.0/24)
	CORSOrigins       string // разрешённые CORS-источники через запятую (например: https://example.com,http://localhost:3000)
	StrictMode        bool   // строгий режим: неверные переменные окружения вызывают ошибку вместо fallback
	ForceSecureCookie bool   // принудительно устанавливать Secure-флаг на куках (даже без TLS)

	// Rate limiting — настраиваются через env с дефолтами из constants.go
	RateLimitWindow         time.Duration // окно rate limiter (по умолчанию 1m)
	RateLimitGlobalRequests int           // глобальный лимит запросов в минуту (по умолчанию 100)
	RateLimitLoginRequests  int           // лимит попыток входа в минуту (по умолчанию 5)
	RateLimitRegistration   int           // лимит регистраций в минуту (по умолчанию 3)
	RateLimitCodeSubmission int           // лимит отправки кода в минуту (по умолчанию 10)
	RateLimitSSE            int           // лимит SSE-соединений в минуту (по умолчанию 10)
	RateLimitAPI            int           // лимит API-запросов в минуту (по умолчанию 60)
	RateLimitPasswordReset  int           // лимит сбросов пароля в минуту (по умолчанию 5)

	// WebAuthn (passkeys) — origins через запятую, RPID по умолчанию из BaseURL
	WebAuthnRPID    string // relying party ID (например: localhost, example.com)
	WebAuthnOrigins string // разрешённые origins через запятую (например: http://localhost:8080,http://127.0.0.1:8080)

	LogLevel string // уровень логирования: debug, info, warn, error (по умолчанию info)
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
	Host         string // хост Valkey
	Port         string // порт Valkey
	Password     string // пароль (если требуется)
	PoolSize     int    // максимальное количество соединений в пуле
	MinIdleConns int    // минимальное количество idle соединений
	MaxRetries   int    // максимальное количество повторных попыток
}

const (
	defaultValkeyPoolSize     = 20
	defaultValkeyMinIdleConns = 5
	defaultValkeyMaxRetries   = 3
)

// JWTConfig содержит параметры JWT-аутентификации.
type JWTConfig struct {
	Secret        string        // секретный ключ для подписи токенов (минимум 32 символа)
	AccessExpiry  time.Duration // срок действия access-токена (по умолчанию 15 минут)
	RefreshExpiry time.Duration // срок действия refresh-токена (по умолчанию 7 дней)
}

// SessionConfig содержит параметры сессий.
type SessionConfig struct {
	Secret string // секретный ключ для подписи cookie сессии (минимум 32 символа)

	// CSRFSecret — отдельный ключ для CSRF-токенов (S-42-4 pass 42).
	// Если не задан — используется Session.Secret (совместимость).
	CSRFSecret string
}

// AdminConfig содержит учётные данные администратора, создаваемого при инициализации.
type AdminConfig struct {
	Email    string // email администратора
	Password string // пароль администратора (должен быть не менее 12 символов)
}

// OAuthConfig содержит конфигурацию OAuth-провайдеров.
type OAuthConfig struct {
	Yandex OAuthProvider // настройки Yandex OAuth
	VK     OAuthProvider // настройки VK OAuth
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

// VAPIDConfig содержит VAPID-ключи для Web Push.
type VAPIDConfig struct {
	PublicKey  string // публичный ключ (для браузера)
	PrivateKey string // приватный ключ (для подписи push)
	Subject    string // contact info для VAPID JWT (mailto: или https://)
}

// LoadConfig загружает конфигурацию из переменных окружения с жёсткой проверкой обязательных секретов.
// Выполняет проверки и возвращает конфигурацию или ошибку.
func LoadConfig() (*Config, error) {
	cfg := &Config{}

	// Сервер
	cfg.Server.Port = getEnvOrDefault("PORT", "8080")
	cfg.Server.GinMode = getEnvOrDefault("GIN_MODE", "release")
	cfg.Server.BaseURL = getEnvOrDefault("BASE_URL", "http://localhost:"+cfg.Server.Port)
	cfg.Server.MaxBackups = getEnvAsInt("LOG_MAX_BACKUPS", defaultMaxBackups)
	cfg.Server.LogFilePath = getEnvOrDefault("LOG_FILE_PATH", "logs/app.log")
	cfg.Server.LogMaxSize = getEnvAsInt("LOG_MAX_SIZE", defaultLogMaxSizeMB)
	cfg.Server.LogMaxAge = getEnvAsInt("LOG_MAX_AGE", defaultLogMaxAgeDays)
	cfg.Server.LogCompress = getEnvAsBool("LOG_COMPRESS", true)
	cfg.Server.LogFormat = getEnvOrDefault("LOG_FORMAT", "console") // console или json
	cfg.Server.StaticDir = getEnvOrDefault("STATIC_DIR", "static")
	cfg.Server.UploadsDir = getEnvOrDefault("UPLOADS_DIR", "uploads")
	cfg.Server.MaxUploadSize = getEnvAsInt("MAX_UPLOAD_SIZE", 5<<20)
	cfg.Server.MaxBodySize = getEnvAsInt("MAX_BODY_SIZE", 10<<20)
	cfg.Server.CORSOrigins = getEnvOrDefault("CORS_ORIGINS", "")
	cfg.Server.TrustedProxies = getEnvOrDefault("TRUSTED_PROXIES", "")
	cfg.Server.StrictMode = os.Getenv("STRICT_CONFIG") == "true"

	// Rate limits
	cfg.Server.RateLimitWindow = getEnvAsDuration("RATE_LIMIT_WINDOW", RateLimitWindow)
	cfg.Server.RateLimitGlobalRequests = getEnvAsInt("RATE_LIMIT_GLOBAL", GlobalRateLimit)
	cfg.Server.RateLimitLoginRequests = getEnvAsInt("RATE_LIMIT_LOGIN", LoginRateLimit)
	cfg.Server.RateLimitRegistration = getEnvAsInt("RATE_LIMIT_REGISTRATION", RegistrationRateLimit)
	cfg.Server.RateLimitCodeSubmission = getEnvAsInt("RATE_LIMIT_CODE_SUBMISSION", CodeSubmissionRateLimit)
	cfg.Server.RateLimitSSE = getEnvAsInt("RATE_LIMIT_SSE", SSERateLimit)
	cfg.Server.RateLimitAPI = getEnvAsInt("RATE_LIMIT_API", APIRateLimit)
	cfg.Server.RateLimitPasswordReset = getEnvAsInt("RATE_LIMIT_PASSWORD_RESET", PasswordResetRateLimit)

	// Log level
	cfg.Server.LogLevel = getEnvOrDefault("LOG_LEVEL", "info")

	// Force Secure cookie flag (default false — detected from TLS)
	cfg.Server.ForceSecureCookie = os.Getenv("FORCE_SECURE_COOKIE") == "true"

	// WebAuthn
	cfg.Server.WebAuthnRPID = os.Getenv("WEBAUTHN_RPID")
	cfg.Server.WebAuthnOrigins = os.Getenv("WEBAUTHN_ORIGINS")
	if cfg.Server.WebAuthnRPID == "" {
		log.Warn().Msg("WEBAUTHN_RPID is not set — passkey authentication will fail at runtime. Set WEBAUTHN_RPID to your domain (e.g., example.com)")
	}

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
	cfg.Valkey.PoolSize = getEnvAsInt("VALKEY_POOL_SIZE", defaultValkeyPoolSize)
	cfg.Valkey.MinIdleConns = getEnvAsInt("VALKEY_MIN_IDLE_CONNS", defaultValkeyMinIdleConns)
	cfg.Valkey.MaxRetries = getEnvAsInt("VALKEY_MAX_RETRIES", defaultValkeyMaxRetries)

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

	// Отдельный CSRF-ключ (S-42-4, pass 42): опционален, fallback на
	// SESSION_SECRET. Рекомендуется задавать CSRF_SECRET — компрометация
	// одного ключа не ослабляет второй механизм.
	csrfSecret := os.Getenv("CSRF_SECRET")
	if csrfSecret == "" {
		csrfSecret = cfg.Session.Secret
	}
	cfg.Session.CSRFSecret = csrfSecret

	// Администратор – обязателен
	if cfg.Admin.Email, err = requireEnv("ADMIN_EMAIL"); err != nil {
		return nil, err
	}
	if cfg.Admin.Password, err = requireStrongPassword("ADMIN_PASSWORD", 12); err != nil {
		return nil, err
	}

	// OAuth провайдеры – каждый со своим флагом включения
	if cfg.OAuth.Yandex, err = loadOAuthProvider("YANDEX", cfg.Server.StrictMode); err != nil {
		return nil, err
	}
	if cfg.OAuth.VK, err = loadOAuthProvider("VK", cfg.Server.StrictMode); err != nil {
		return nil, err
	}

	// SMTP
	if cfg.SMTP, err = loadSMTPConfig(cfg.Server.StrictMode); err != nil {
		return nil, err
	}

	// reCAPTCHA
	if cfg.ReCAPTCHA, err = loadReCAPTCHAConfig(cfg.Server.StrictMode); err != nil {
		return nil, err
	}

	// TLS
	cfg.TLS.CertFile = os.Getenv("TLS_CERT_FILE")
	cfg.TLS.KeyFile = os.Getenv("TLS_KEY_FILE")

	// Sentry
	if cfg.Sentry, err = loadSentryConfig(cfg.Server.StrictMode); err != nil {
		return nil, err
	}

	// WebSocket
	cfg.WebSocket.MaxTotalConns = getEnvAsInt("WS_MAX_TOTAL_CONNS", defaultWSMaxTotalConns)
	cfg.WebSocket.MaxConnsPerIP = getEnvAsInt("WS_MAX_CONNS_PER_IP", defaultWSMaxConnsPerIP)

	// VAPID
	if err := loadVAPIDConfig(&cfg.VAPID); err != nil {
		return nil, err
	}

	return cfg, nil
}

// =============================================================================
// Вспомогательные функции (не экспортируются)
// =============================================================================

// stripComments удаляет inline-комментарии (# ...) и обрезает пробелы.
func stripComments(s string) string {
	if idx := strings.Index(s, "#"); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

// getEnvOrDefault возвращает значение переменной окружения или fallback, если переменная не установлена.
func getEnvOrDefault(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return stripComments(value)
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
	for _, w := range commonWeak {
		if strings.EqualFold(value, w) {
			return "", fmt.Errorf("environment variable %s appears to be a weak/default value, please change it", key)
		}
	}
	return value, nil
}

// requireStrongPassword проверяет, что пароль администратора имеет длину не менее minLen
// и содержит минимум 3 из 4 классов символов (заглавные, строчные, цифры, спецсимволы).
func requireStrongPassword(key string, minLen int) (string, error) {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return "", fmt.Errorf("environment variable %s is required (admin password)", key)
	}
	if len(value) < minLen {
		return "", fmt.Errorf("environment variable %s must be at least %d characters long (current: %d)", key, minLen, len(value))
	}
	var upper, lower, digit, special bool
	for _, r := range value {
		switch {
		case r >= 'A' && r <= 'Z':
			upper = true
		case r >= 'a' && r <= 'z':
			lower = true
		case r >= '0' && r <= '9':
			digit = true
		default:
			special = true
		}
	}
	classes := 0
	for _, b := range []bool{upper, lower, digit, special} {
		if b {
			classes++
		}
	}
	if classes < 3 {
		return "", fmt.Errorf("environment variable %s must contain at least 3 of: uppercase, lowercase, digit, special character", key)
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
func loadOAuthProvider(prefix string, strictMode bool) (OAuthProvider, error) {
	enabledEnv := prefix + "_ENABLED"
	enabled, err := parseBoolStrict(enabledEnv, strictMode)
	if err != nil {
		return OAuthProvider{}, err
	}
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
func loadSMTPConfig(strictMode bool) (SMTPConfig, error) {
	enabled, err := parseBoolStrict("SMTP_ENABLED", strictMode)
	if err != nil {
		return SMTPConfig{}, err
	}
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
func loadReCAPTCHAConfig(strictMode bool) (ReCAPTCHAConfig, error) {
	enabled, err := parseBoolStrict("RECAPTCHA_ENABLED", strictMode)
	if err != nil {
		return ReCAPTCHAConfig{}, err
	}
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
func loadSentryConfig(strictMode bool) (SentryConfig, error) {
	enabled, err := parseBoolStrict("SENTRY_ENABLED", strictMode)
	if err != nil {
		return SentryConfig{}, err
	}
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

// parseBoolStrict парсит булево значение переменной окружения.
// Если переменная не установлена, возвращает false без ошибки.
// Если значение неверное и strictMode=true, возвращает ошибку.
// Если значение неверное и strictMode=false, логирует предупреждение и возвращает false.
func parseBoolStrict(envName string, strictMode bool) (bool, error) {
	val := os.Getenv(envName)
	if val == "" {
		return false, nil
	}
	parsed, err := strconv.ParseBool(val)
	if err != nil {
		if strictMode {
			return false, fmt.Errorf("неверное значение %s=%q: %w", envName, val, err)
		}
		log.Warn().Err(err).Str("env", envName).Str("value", val).Msg("неверное значение bool, используется false")
		return false, nil
	}
	return parsed, nil
}

// getEnvAsDuration возвращает значение переменной окружения как time.Duration или fallback при ошибке.
func getEnvAsDuration(key string, fallback time.Duration) time.Duration {
	if value, ok := os.LookupEnv(key); ok {
		if d, err := time.ParseDuration(value); err == nil {
			return d
		}
	}
	return fallback
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

// loadVAPIDConfig загружает VAPID-ключи из env или генерирует новые.
func loadVAPIDConfig(cfg *VAPIDConfig) error {
	cfg.PublicKey = os.Getenv("VAPID_PUBLIC_KEY")
	cfg.PrivateKey = os.Getenv("VAPID_PRIVATE_KEY")
	cfg.Subject = os.Getenv("VAPID_SUBJECT")
	if cfg.Subject == "" {
		cfg.Subject = "mailto:admin@encounter-engine.local"
	}
	if cfg.PublicKey != "" && cfg.PrivateKey != "" {
		return nil
	}
	log.Warn().Msg("VAPID keys not found in env, generating new ones — push notifications will reset on restart")
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return fmt.Errorf("failed to generate VAPID keys: %w", err)
	}
	cfg.PrivateKey = privateKey
	cfg.PublicKey = publicKey
	return nil
}
