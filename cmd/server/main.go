// cmd/server/main.go
package main

import (
	"context"
	"errors"
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
	// Загрузка .env файла
	if err := godotenv.Load(); err != nil {
		if !os.IsNotExist(err) {
			log.Fatal().Err(err).Msg("Ошибка при загрузке .env файла")
		}
		log.Info().Msg("Файл .env не найден, используются только системные переменные окружения")
	}

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	// --- Загрузка конфигурации ---
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatal().Err(err).Msg("Не удалось загрузить конфигурацию")
	}

	// --- Настройка логгера с ротацией ---
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
	multi := zerolog.MultiLevelWriter(
		zerolog.ConsoleWriter{Out: os.Stderr},
		logFile,
	)
	log.Logger = log.Output(multi)

	log.Info().Str("version", version).Str("build", buildDate).Msg("Запуск сервера")
	log.Info().
		Str("log_file", logFilePath).
		Int("max_size_mb", cfg.Server.LogMaxSize).
		Int("max_backups", cfg.Server.MaxBackups).
		Int("max_age_days", cfg.Server.LogMaxAge).
		Bool("compress", cfg.Server.LogCompress).
		Msg("Ротация логов включена")

	gin.SetMode(cfg.Server.GinMode)

	// --- Подключение к БД с повторными попытками ---
	database, err := connectDBWithRetry(cfg, 5, 2*time.Second)
	if err != nil {
		log.Fatal().Err(err).Msg("Не удалось подключиться к БД после нескольких попыток")
	}
	log.Info().Msg("Подключение к БД установлено")

	// Применение миграций (критично)
	if err := db.MigrateFromFiles(database, "migrations"); err != nil {
		log.Fatal().Err(err).Msg("Ошибка применения миграций")
	}
	log.Info().Msg("Миграции применены")

	// Создание/обновление администратора
	if err := db.EnsureAdmin(database, cfg); err != nil {
		log.Fatal().Err(err).Msg("Не удалось создать/обновить администратора")
	}

	localStorage := storage.NewLocalStorage()
	hub := ws.NewRoomHub()
	go hub.Run()

	// --- СОЗДАНИЕ ЗАВИСИМОСТЕЙ И APP (обновлено) ---
	deps := app.NewDependencies(database, cfg, hub)
	appInstance := app.NewApp(database, localStorage, hub, cfg, ".", deps)

	r, err := appInstance.SetupRouter()
	if err != nil {
		log.Fatal().Err(err).Msg("Не удалось настроить маршруты")
	}

	// Контекст для фоновых задач (отменяется при завершении)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Запуск фоновых задач
	go game.CheckTimeouts(database, ctx)
	go game.CheckAutoStartGames(database, ctx)

	// --- Создание HTTP-сервера с таймаутами ---
	srv := &http.Server{
		Addr:         ":" + cfg.Server.Port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Запуск сервера в горутине
	go func() {
		log.Info().Str("port", cfg.Server.Port).Msg("Сервер запущен")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("Ошибка работы сервера")
		}
	}()

	// Ожидание сигналов завершения
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info().Msg("Получен сигнал завершения, инициируем graceful shutdown...")

	// Даём 5 секунд на завершение текущих запросов
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("Ошибка при завершении сервера")
	}

	// Отменяем контекст фоновых задач
	cancel()

	log.Info().Msg("Сервер остановлен")
}

// connectDBWithRetry пытается подключиться к БД с экспоненциальной задержкой.
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
