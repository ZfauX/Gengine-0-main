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
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	_ "gengine-0/internal/pkg/metrics"

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
	// НАСТРОЙКА ЛОГГЕРА С ПОДДЕРЖКОЙ JSON / CONSOLE
	// ============================================================
	logFilePath := cfg.Server.LogFilePath
	if logFilePath == "" {
		logFilePath = "logs/app.log"
	}
	if err := os.MkdirAll(filepath.Dir(logFilePath), 0755); err != nil {
		log.Fatal().Err(err).Msg("Не удалось создать директорию для логов")
	}

	// Настройка ротации логов
	logFile := &lumberjack.Logger{
		Filename:   logFilePath,
		MaxSize:    cfg.Server.LogMaxSize,
		MaxBackups: cfg.Server.MaxBackups,
		MaxAge:     cfg.Server.LogMaxAge,
		Compress:   cfg.Server.LogCompress,
	}

	// Выбор формата вывода
	var consoleWriter zerolog.ConsoleWriter
	if cfg.Server.LogFormat == "json" {
		// JSON-формат — пишем в stdout и в файл без форматирования
		multi := zerolog.MultiLevelWriter(
			os.Stderr, // JSON в stderr
			logFile,
		)
		log.Logger = zerolog.New(multi).With().Timestamp().Logger()
	} else {
		// Консольный формат (по умолчанию)
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

	email.InitQueue(cfg, 5, 100)

	appCache := cache.NewCache(10*time.Minute, 5*time.Minute)

	deps := app.NewDependencies(database, cfg, hub, localStorage, appCache)
	appInstance := app.NewApp(database, localStorage, hub, cfg, ".", deps)

	r, err := appInstance.SetupRouter()
	if err != nil {
		log.Fatal().Err(err).Msg("Не удалось настроить маршруты")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go game.CheckTimeouts(database, ctx)
	go game.CheckAutoStartGames(database, ctx)

	srv := &http.Server{
		Addr:         ":" + cfg.Server.Port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Info().Str("port", cfg.Server.Port).Msg("Сервер запущен")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("Ошибка работы сервера")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info().Msg("Получен сигнал завершения, инициируем graceful shutdown...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("Ошибка при завершении сервера")
	}

	cancel()

	email.ShutdownQueue()

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
