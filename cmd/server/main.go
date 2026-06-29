// cmd/server/main.go
package main

import (
	"context"
	"os"
	"path/filepath"

	"gengine-0/internal/app"
	"gengine-0/internal/config"
	"gengine-0/internal/db"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	_ "gengine-0/internal/pkg/metrics" // +++ инициализация метрик

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gopkg.in/natefinch/lumberjack.v2"
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
	// Загрузка .env файла с улучшенной обработкой ошибок
	if err := godotenv.Load(); err != nil {
		// Если ошибка не связана с отсутствием файла, прерываем выполнение
		if !os.IsNotExist(err) {
			log.Fatal().Err(err).Msg("Ошибка при загрузке .env файла")
		}
		log.Info().Msg("Файл .env не найден, используются только системные переменные окружения")
	}

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	// --- Настройка логгера с ротацией ---
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatal().Err(err).Msg("Не удалось загрузить конфигурацию")
	}

	// Путь к файлу логов (можно переопределить через переменную окружения LOG_FILE_PATH)
	logFilePath := os.Getenv("LOG_FILE_PATH")
	if logFilePath == "" {
		logFilePath = "logs/app.log"
	}
	// Создаём директорию для логов, если её нет
	if err := os.MkdirAll(filepath.Dir(logFilePath), 0755); err != nil {
		log.Fatal().Err(err).Msg("Не удалось создать директорию для логов")
	}

	// Настройка ротации через lumberjack
	// MaxSize: 100 МБ (можно вынести в конфиг при необходимости)
	// MaxAge: 28 дней (можно вынести в конфиг)
	// MaxBackups: берём из конфига (cfg.Server.MaxBackups)
	// Compress: включим сжатие архивов для экономии места
	logFile := &lumberjack.Logger{
		Filename:   logFilePath,
		MaxSize:    100, // мегабайт
		MaxBackups: cfg.Server.MaxBackups,
		MaxAge:     28, // дней
		Compress:   true,
	}

	// Пишем логи одновременно в консоль и в файл
	multi := zerolog.MultiLevelWriter(
		zerolog.ConsoleWriter{Out: os.Stderr},
		logFile,
	)
	log.Logger = log.Output(multi)

	log.Info().Str("version", version).Str("build", buildDate).Msg("Запуск сервера")
	log.Info().Str("log_file", logFilePath).Int("max_backups", cfg.Server.MaxBackups).Msg("Ротация логов включена")

	gin.SetMode(cfg.Server.GinMode)

	database, err := db.Connect(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("Не удалось подключиться к БД")
	}

	if err := db.MigrateFromFiles(database, "migrations"); err != nil {
		log.Fatal().Err(err).Msg("Ошибка применения миграций")
	}

	if err := db.EnsureAdmin(database, cfg); err != nil {
		log.Fatal().Err(err).Msg("Не удалось создать/обновить администратора")
	}

	localStorage := storage.NewLocalStorage()
	hub := ws.NewRoomHub()
	go hub.Run()

	// Создаём экземпляр App и настраиваем роутер (новый подход)
	appInstance := app.NewApp(database, localStorage, hub, cfg, ".")
	r, err := appInstance.SetupRouter()
	if err != nil {
		log.Fatal().Err(err).Msg("Не удалось настроить маршруты")
	}

	// Запускаем фоновые задачи
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go game.CheckTimeouts(database, ctx)
	go game.CheckAutoStartGames(database, ctx)

	// Запускаем HTTP-сервер
	app.RunServer(r, database, cfg, cancel)
}
