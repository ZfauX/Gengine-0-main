// cmd/server/main.go
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"gengine-0/internal/app"
	"gengine-0/internal/config"
	"gengine-0/internal/db"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/pkg/cache"
	"gengine-0/internal/pkg/email"
	"gengine-0/internal/pkg/logging"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gopkg.in/natefinch/lumberjack.v2"
	"gorm.io/gorm"
)

// @title Gengine API
// @version 1.0
// @description API для платформы квестов Gengine
// @termsOfService http://swagger.io/terms/
// @contact.name API Support
// @contact.email support@gengine.io
// @license.name MIT
// @license.url https://opensource.org/licenses/MIT
// @host localhost:8080
// @BasePath /
// @securityDefinitions.apikey JWT
// @in cookie
// @name jwt

var (
	version   = "dev"
	buildDate = "unknown"
)

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	migrateFlag := flag.Bool("migrate", false, "Применить миграции и выйти")
	flag.Parse()

	if err := godotenv.Load(); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to load .env file: %w", err)
		}
		log.Info().Msg(".env file not found, using only system environment variables")
	}

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// ============================================================
	// ИНИЦИАЛИЗАЦИЯ SENTRY
	// ============================================================
	var sentryWriter *logging.SentryWriter
	sentryFlushTimeout := config.SentryFlushTimeout
	if cfg.Sentry.Enabled && cfg.Sentry.DSN != "" {
		sentryErr := sentry.Init(sentry.ClientOptions{
			Dsn:              cfg.Sentry.DSN,
			TracesSampleRate: cfg.Sentry.TracingRate,
			Release:          version,
			Environment:      cfg.Server.GinMode,
		})
		if sentryErr != nil {
			log.Warn().Err(sentryErr).Msg("Sentry: initialization failed, continuing without Sentry")
		} else {
			log.Info().Msg("Sentry: initialized successfully")
			defer sentry.Flush(sentryFlushTimeout)
			sentryWriter = logging.NewSentryWriter(sentryFlushTimeout)
		}
	} else {
		log.Info().Msg("Sentry: disabled")
	}

	// ============================================================
	// НАСТРОЙКА ЛОГГЕРА
	// ============================================================
	logFilePath := cfg.Server.LogFilePath
	if logFilePath == "" {
		logFilePath = "logs/app.log"
	}
	if mkdirErr := os.MkdirAll(filepath.Dir(logFilePath), 0755); mkdirErr != nil {
		log.Error().Err(mkdirErr).Msg("failed to create log directory")
		return fmt.Errorf("failed to create log directory: %w", mkdirErr)
	}

	logFile := &lumberjack.Logger{
		Filename:   logFilePath,
		MaxSize:    cfg.Server.LogMaxSize,
		MaxBackups: cfg.Server.MaxBackups,
		MaxAge:     cfg.Server.LogMaxAge,
		Compress:   cfg.Server.LogCompress,
	}

	writers := []io.Writer{os.Stderr, logFile}
	if sentryWriter != nil {
		writers = append(writers, sentryWriter)
	}

	var consoleWriter zerolog.ConsoleWriter
	if cfg.Server.LogFormat == "json" {
		log.Logger = zerolog.New(zerolog.MultiLevelWriter(writers...)).With().Timestamp().Logger()
	} else {
		consoleWriter = zerolog.ConsoleWriter{Out: os.Stderr}
		writers[0] = consoleWriter
		log.Logger = log.Output(zerolog.MultiLevelWriter(writers...))
	}

	log.Info().
		Str("version", version).
		Str("build", buildDate).
		Str("log_format", cfg.Server.LogFormat).
		Msg("Запуск сервера")
	log.Info().
		Str("log_file", logFilePath).
		Int("max_size_mb", cfg.Server.LogMaxSize).
		Int("max_backups", cfg.Server.MaxBackups).
		Int("max_age_days", cfg.Server.LogMaxAge).
		Bool("compress", cfg.Server.LogCompress).
		Msg("Ротация логов включена")

	gin.SetMode(cfg.Server.GinMode)

	// --- Подключение к БД ---
	database, err := connectDBWithRetry(cfg, config.DBMaxRetryAttempts, config.DBRetryInitialDelay)
	if err != nil {
		log.Error().Err(err).Msg("failed to connect to DB after several attempts")
		return fmt.Errorf("failed to connect to DB after several attempts: %w", err)
	}
	log.Info().Msg("DB connection established")

	if *migrateFlag {
		log.Info().Msg("running migrations...")
		if migrateErr := db.RunMigrations(database); migrateErr != nil {
			log.Error().Err(migrateErr).Msg("migration error")
			return fmt.Errorf("migration error: %w", migrateErr)
		}
		log.Info().Msg("migrations applied successfully")
		return nil
	}

	if ensureErr := db.EnsureAdmin(database, cfg); ensureErr != nil {
		log.Error().Err(ensureErr).Msg("failed to create/update admin")
		return fmt.Errorf("failed to create/update admin: %w", ensureErr)
	}

	localStorage := storage.NewLocalStorage().WithBaseDir(filepath.Join(".", cfg.Server.UploadsDir))
	hub := ws.NewRoomHub()
	hub.SetLimits(cfg.WebSocket.MaxTotalConns, cfg.WebSocket.MaxConnsPerIP)
	go hub.Run()

	// --- Инициализация rate limiters (singleton, создаются один раз) ---
	// Если Valkey доступен, используем его как shared backend для rate limiters между инстансами
	if cfg.Valkey.Host != "" {
		valkeyClient := cache.NewValkeyClient(cfg.Valkey.Host, cfg.Valkey.Port, cfg.Valkey.Password, cfg.Valkey.PoolSize, cfg.Valkey.MinIdleConns, cfg.Valkey.MaxRetries)
		if valkeyClient != nil {
			middleware.InitGlobalRateLimiterWithValkey(valkeyClient, config.RateLimitWindow, config.GlobalRateLimit)
			middleware.InitLoginRateLimiterWithValkey(valkeyClient, config.RateLimitWindow, config.LoginRateLimit)
			middleware.InitRegistrationRateLimiterWithValkey(valkeyClient, config.RateLimitWindow, config.RegistrationRateLimit)
			middleware.InitCodeSubmissionRateLimiterWithValkey(valkeyClient, config.RateLimitWindow, config.CodeSubmissionRateLimit)
			middleware.InitSSERateLimiterWithValkey(valkeyClient, config.RateLimitWindow, config.SSERateLimit)
			middleware.InitAPIRateLimiterWithValkey(valkeyClient, config.RateLimitWindow, config.APIRateLimit)
		}
	} else {
		middleware.InitGlobalRateLimiter(config.RateLimitWindow, config.GlobalRateLimit)
		middleware.InitLoginRateLimiter(config.RateLimitWindow, config.LoginRateLimit)
		middleware.InitRegistrationRateLimiter(config.RateLimitWindow, config.RegistrationRateLimit)
		middleware.InitCodeSubmissionRateLimiter(config.RateLimitWindow, config.CodeSubmissionRateLimit)
		middleware.InitSSERateLimiter(config.RateLimitWindow, config.SSERateLimit)
		middleware.InitAPIRateLimiter(config.RateLimitWindow, config.APIRateLimit)
	}

	// --- Инициализация persistent-очереди email (только если SMTP включён) ---
	if cfg.SMTP.Enabled {
		email.InitQueue(cfg, database, config.EmailQueueWorkers, config.EmailQueueInterval, config.EmailQueueBatchSize)
	} else {
		log.Info().Msg("SMTP disabled, email queue not started")
	}

	// --- Инициализация кэша (Valkey с fallback на in-memory, NoopCache как последний fallback) ---
	var appCache cache.CacheStore
	if cfg.Valkey.Host != "" {
		appCache = cache.NewValkeyCache(cfg.Valkey.Host, cfg.Valkey.Port, cfg.Valkey.Password, cfg.Valkey.PoolSize, cfg.Valkey.MinIdleConns, cfg.Valkey.MaxRetries)
		if appCache == nil {
			log.Warn().Msg("Valkey unavailable, using in-memory cache")
			appCache, err = cache.NewCache(config.CacheDefaultTTL, config.CacheCleanupInterval)
			if err != nil {
				return fmt.Errorf("failed to create in-memory cache: %w", err)
			}
		}
	} else {
		log.Info().Msg("Valkey not configured, using in-memory cache")
		appCache, err = cache.NewCache(config.CacheDefaultTTL, config.CacheCleanupInterval)
		if err != nil {
			return fmt.Errorf("failed to create in-memory cache: %w", err)
		}
	}

	deps := app.NewDependencies(database, cfg, hub, localStorage, appCache)
	appInstance := app.NewApp(database, localStorage, hub, cfg, ".", deps)
	r, err := appInstance.SetupRouter()
	if err != nil {
		log.Error().Err(err).Msg("failed to setup routes")
		return fmt.Errorf("failed to setup routes: %w", err)
	}

	// Контекст для фоновых задач
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Callback для расчёта результатов при завершении игры
	onGameFinished := func(ctx context.Context, gameID uint) {
		if deps.Services.Monitor != nil {
			if err := deps.Services.Monitor.CalculateResults(ctx, gameID); err != nil {
				log.Error().Err(err).Uint("game_id", gameID).Msg("onGameFinished: CalculateResults failed")
			}
		}
	}

	// goSafe запускает горутину с recover.
	goSafe := func(fn func()) {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Error().Interface("panic", r).Str("stack", string(debug.Stack())).Msg("goroutine panicked")
				}
			}()
			fn()
		}()
	}

	// Запуск фоновых задач
	goSafe(func() { game.CheckTimeouts(database, ctx, onGameFinished) })
	goSafe(func() { game.CheckAutoStartGames(database, ctx) })

	// Мониторинг connection pool (раз в минуту)
	goSafe(func() {
		ticker := time.NewTicker(config.PoolMonitorInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Info().Msg("connection pool monitoring: stopping")
				return
			case <-ticker.C:
				sqlDB, err := database.DB()
				if err != nil {
					log.Warn().Err(err).Msg("connection pool monitoring: failed to get sql.DB")
					continue
				}
				stats := sqlDB.Stats()
				log.Debug().
					Int("open_connections", stats.OpenConnections).
					Int("in_use", stats.InUse).
					Int("idle", stats.Idle).
					Int64("wait_count", stats.WaitCount).
					Int64("wait_duration_ms", stats.WaitDuration.Milliseconds()).
					Msg("Connection pool stats")
			}
		}
	})

	// WebSocket cleanup — периодическая очистка неактивных соединений
	goSafe(func() { appInstance.Hub.StartCleanupPeriodic() })

	// Фоновая очистка просроченных refresh-токенов (раз в час)
	goSafe(func() {
		ticker := time.NewTicker(config.RefreshTokenCleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Info().Msg("refresh token cleanup: context cancelled, stopping")
				return
			case <-ticker.C:
				if err := deps.Services.Auth.CleanExpiredRefreshTokens(ctx); err != nil {
					log.Error().Err(err).Msg("Очистка refresh-токенов: ошибка")
				} else {
					log.Debug().Msg("Очистка refresh-токенов: успешно завершена")
				}
			}
		}
	})

	srv := &http.Server{
		Addr:         ":" + cfg.Server.Port,
		Handler:      r,
		ReadTimeout:  config.ServerReadTimeout,
		WriteTimeout: config.ServerWriteTimeout,
		IdleTimeout:  config.ServerIdleTimeout,
	}

	goSafe(func() {
		log.Info().Str("port", cfg.Server.Port).Msg("Сервер запущен")
		var err error
		if cfg.TLS.CertFile != "" && cfg.TLS.KeyFile != "" {
			err = srv.ListenAndServeTLS(cfg.TLS.CertFile, cfg.TLS.KeyFile)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("Ошибка работы сервера")
		}
	})

	// Ожидание сигналов завершения
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info().Msg("Получен сигнал завершения, инициируем graceful shutdown...")

	// ============================================================
	// GRACEFUL SHUTDOWN — правильный порядок:
	// 1. Остановить rate limiters (перестать принимать новые запросы)
	// 2. Остановить HTTP-сервер (дождаться завершения текущих запросов, включая WS upgrade)
	// 3. Остановить WebSocket-хаб (после HTTP — больше нет активных хендлеров)
	// 4. Отменить контекст фоновых задач
	// 5. Остановить email-очередь
	// 6. Закрыть кэш (Valkey)
	// ============================================================

	// 1. Останавливаем rate limiters — запрещаем новые запросы
	middleware.StopGlobalRateLimiter()
	middleware.StopLoginRateLimiter()
	middleware.StopRegistrationRateLimiter()
	middleware.StopCodeSubmissionRateLimiter()
	middleware.StopSSERateLimiter()
	middleware.StopAPIRateLimiter()

	// 2. Останавливаем HTTP-сервер (ожидаем завершения текущих запросов)
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("Ошибка при завершении HTTP-сервера")
	}
	log.Info().Msg("HTTP-сервер остановлен")

	// 3. Останавливаем WebSocket-хаб (после HTTP — ни один хендлер не использует хаб)
	hub.Stop()
	log.Info().Msg("WebSocket-хаб остановлен")

	// 4. Отменяем контекст фоновых задач
	cancel()
	log.Info().Msg("Контекст отменён, фоновые задачи останавливаются")

	// 5. Останавливаем очередь email (если была запущена)
	if cfg.SMTP.Enabled {
		email.ShutdownQueue()
	}

	// 6. Закрываем кэш (Valkey connection)
	if closer, ok := appCache.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			log.Warn().Err(err).Msg("Ошибка закрытия кэша")
		} else {
			log.Info().Msg("Кэш закрыт")
		}
	}

	log.Info().Msg("Сервер полностью остановлен")
	return nil
}

func connectDBWithRetry(cfg *config.Config, maxAttempts int, initialDelay time.Duration) (*gorm.DB, error) {
	var dbConn *gorm.DB
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		log.Info().Int("attempt", attempt).Msg("Попытка подключения к БД")
		dbConn, lastErr = db.Connect(cfg)
		if lastErr == nil {
			return dbConn, nil
		}

		if attempt == maxAttempts {
			break
		}

		delay := initialDelay * time.Duration(1<<(attempt-1))
		log.Warn().
			Err(lastErr).
			Dur("delay", delay).
			Int("remaining", maxAttempts-attempt).
			Msg("Ошибка подключения к БД, повтор через задержку")
		time.Sleep(delay)
	}

	return nil, fmt.Errorf("не удалось подключиться к БД после %d попыток: %w", maxAttempts, lastErr)
}
