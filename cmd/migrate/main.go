// cmd/migrate/main.go
package main

import (
	"flag"
	"os"
	"path/filepath"

	"gengine-0/internal/config"
	"gengine-0/internal/db"

	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gopkg.in/natefinch/lumberjack.v2"
)

func main() {
	// Определяем флаги
	migrationsDir := flag.String("dir", "migrations", "Папка с файлами миграций")
	create := flag.String("create", "", "Создать новый файл миграции с указанным именем")
	flag.Parse()

	// Загрузка .env файла
	if err := godotenv.Load(); err != nil {
		if !os.IsNotExist(err) {
			log.Fatal().Err(err).Msg("Ошибка при загрузке .env файла")
		}
		log.Info().Msg("Файл .env не найден, используются только системные переменные окружения")
	}

	// Настройка логгера
	logFilePath := "logs/migrate.log"
	if err := os.MkdirAll(filepath.Dir(logFilePath), 0755); err != nil {
		log.Fatal().Err(err).Msg("Не удалось создать директорию для логов")
	}

	logFile := &lumberjack.Logger{
		Filename:   logFilePath,
		MaxSize:    100,
		MaxBackups: 3,
		MaxAge:     28,
		Compress:   true,
	}
	multi := zerolog.MultiLevelWriter(
		zerolog.ConsoleWriter{Out: os.Stderr},
		logFile,
	)
	log.Logger = log.Output(multi)

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	// Загрузка конфигурации
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatal().Err(err).Msg("Не удалось загрузить конфигурацию")
	}

	// Если указан флаг -create, создаём файл миграции
	if *create != "" {
		upPath, downPath, err := db.CreateMigrationFile(*migrationsDir, *create)
		if err != nil {
			log.Fatal().Err(err).Msg("Не удалось создать файл миграции")
		}
		log.Info().Str("up", upPath).Str("down", downPath).Msg("Файлы миграции созданы")
		return
	}

	// Подключение к БД
	database, err := db.Connect(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("Не удалось подключиться к БД")
	}
	log.Info().Msg("Подключение к БД установлено")

	// Применение миграций
	if err := db.RunMigrations(database, *migrationsDir); err != nil {
		log.Fatal().Err(err).Msg("Ошибка применения миграций")
	}

	log.Info().Msg("Миграции успешно применены")
}
