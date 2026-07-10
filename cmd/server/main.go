// cmd/server/main.go
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"gengine-0/internal/app"
	"gengine-0/internal/config"
	"gengine-0/internal/db"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/pkg/cache"
	"gengine-0/internal/pkg/email"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	_ "gengine-0/internal/pkg/metrics"

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
	migrateFlag := flag.Bool("migrate", false, "Применить миграции и выйти")
	flag.Parse()

	if err := godotenv.Load(); err != nil {
		if !os.IsNotExist(err) {
			log.Fatal().Err(err).Msg("Ошибка при загрузке .env файла")
		}
		log.Info().Msg("Файл .env не найден, используются только системные переменные окружения")
	}

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatal().Err(err).Msg("Не удалось загрузить конфигурацию")
	}

	// ============================================================
	// ИНИЦИАЛИЗАЦИЯ SENTRY
	// ============================================================
	if cfg.Sentry.Enabled && cfg.Sentry.DSN != "" {
		err := sentry.Init(sentry.ClientOptions{
			Dsn:              cfg.Sentry.DSN,
			TracesSampleRate: cfg.Sentry.TracingRate,
			Release:          version,
			Environment:      cfg.Server.GinMode,
		})
		if err != nil {
			log.Warn().Err(err).Msg("Sentry: initialization failed, continuing without Sentry")
		} else {
			log.Info().Msg("Sentry: initialized successfully")
			defer sentry.Flush(2 * time.Second)
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
	if err := os.MkdirAll(filepath.Dir(logFilePath), 0755); err != nil {
		log.Fatal().Err(err).Msg("Не удалось создать директорию для логов")
	}

	logFile := &lumberjack.Logger{
		Filename:   logFilePath,
		MaxSize:    cfg.Server.LogMaxSize,
		MaxBackups: cfg.Server.MaxBackups,
		MaxAge:     cfg.Server.LogMaxAge,
		Compress:   cfg.Server.LogCompress,
	}

	var consoleWriter zerolog.ConsoleWriter
	if cfg.Server.LogFormat == "json" {
		multi := zerolog.MultiLevelWriter(
			os.Stderr,
			logFile,
		)
		log.Logger = zerolog.New(multi).With().Timestamp().Logger()
	} else {
		consoleWriter = zerolog.ConsoleWriter{Out: os.Stderr}
		multi := zerolog.MultiLevelWriter(
			consoleWriter,
			logFile,
		)
		log.Logger = log.Output(multi)
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
	database, err := connectDBWithRetry(cfg, 5, 2*time.Second)
	if err != nil {
		log.Fatal().Err(err).Msg("Не удалось подключиться к БД после нескольких попыток")
	}
	log.Info().Msg("Подключение к БД установлено")

	if *migrateFlag {
		log.Info().Msg("Запуск миграций...")
		if err := db.RunMigrations(database, "migrations"); err != nil {
			log.Fatal().Err(err).Msg("Ошибка применения миграций")
		}
		log.Info().Msg("Миграции успешно применены")
		return
	}

	if err := db.EnsureAdmin(database, cfg); err != nil {
		log.Fatal().Err(err).Msg("Не удалось создать/обновить администратора")
	}

	localStorage := storage.NewLocalStorage()
	hub := ws.NewRoomHub()
	go hub.Run()

	// --- Инициализация rate limiters (singleton, создаются один раз) ---
	middleware.InitGlobalRateLimiter(1*time.Minute, 100)
	middleware.InitLoginRateLimiter(1*time.Minute, 5)
	middleware.InitRegistrationRateLimiter(1*time.Minute, 3)

	// --- Инициализация persistent-очереди email (только если SMTP включён) ---
	if cfg.SMTP.Enabled {
		email.InitQueue(cfg, database, 5, 10*time.Second, 10)
	} else {
		log.Info().Msg("SMTP отключён, email-очередь не запущена")
	}

	// --- Инициализация кэша (Valkey с fallback на in-memory) ---
	var appCache cache.CacheStore
	if cfg.Valkey.Host != "" {
		appCache = cache.NewValkeyCache(cfg.Valkey.Host, cfg.Valkey.Port, cfg.Valkey.Password)
		if appCache == nil {
			log.Warn().Msg("Valkey недоступен, используется in-memory кэш")
			appCache = cache.NewCache(10*time.Minute, 5*time.Minute)
		}
	} else {
		log.Info().Msg("Valkey не настроен, используется in-memory кэш")
		appCache = cache.NewCache(10*time.Minute, 5*time.Minute)
	}

	deps := app.NewDependencies(database, cfg, hub, localStorage, appCache)
	appInstance := app.NewApp(database, localStorage, hub, cfg, ".", deps)
	r, err := appInstance.SetupRouter()
	if err != nil {
		log.Fatal().Err(err).Msg("Не удалось настроить маршруты")
	}

	// Контекст для фоновых задач
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Запуск фоновых задач
	go game.CheckTimeouts(database, ctx)
	go game.CheckAutoStartGames(database, ctx)

	// Мониторинг connection pool (раз в минуту)
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Info().Msg("Мониторинг connection pool: остановка")
				return
			case <-ticker.C:
				sqlDB, err := database.DB()
				if err != nil {
					log.Warn().Err(err).Msg("Мониторинг connection pool: не удалось получить sql.DB")
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
	}()

	// WebSocket cleanup — периодическая очистка неактивных соединений
	go appInstance.Hub.StartCleanupPeriodic()

	// Фоновая очистка просроченных refresh-токенов (раз в час)
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Info().Msg("Очистка refresh-токенов: контекст отменён, остановка")
				return
			case <-ticker.C:
				if err := deps.Services.Auth.CleanExpiredRefreshTokens(ctx); err != nil {
					log.Error().Err(err).Msg("Очистка refresh-токенов: ошибка")
				} else {
					log.Debug().Msg("Очистка refresh-токенов: успешно завершена")
				}
			}
		}
	}()

	srv := &http.Server{
		Addr:         ":" + cfg.Server.Port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Info().Str("port", cfg.Server.Port).Msg("Сервер запущен")
		var err error
		// Если TLS сертификаты указаны, запускаем с HTTPS
		if cfg.TLS.CertFile != "" && cfg.TLS.KeyFile != "" {
			err = srv.ListenAndServeTLS(cfg.TLS.CertFile, cfg.TLS.KeyFile)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("Ошибка работы сервера")
		}
	}()

	// Ожидание сигналов завершения
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info().Msg("Получен сигнал завершения, инициируем graceful shutdown...")

	// Останавливаем WebSocket-хаб (отправляет CloseMessage всем клиентам)
	hub.Stop()

	// Останавливаем rate limiters
	middleware.StopGlobalRateLimiter()
	middleware.StopLoginRateLimiter()
	middleware.StopRegistrationRateLimiter()

	// Даём время на завершение обработки запросов
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("Ошибка при завершении сервера")
	}

	// Отменяем контекст фоновых задач
	cancel()

	// Останавливаем очередь email (если была запущена)
	if cfg.SMTP.Enabled {
		email.ShutdownQueue()
	}

	log.Info().Msg("Сервер остановлен")
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
