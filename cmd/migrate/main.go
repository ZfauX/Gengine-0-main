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
	// Определяем флаги.
	// -dir: явная папка миграций. Пустая (по умолчанию) — канонический путь:
	// RunMigrations → автодетект (migrations_squashed для свежей/сгруппированной
	// БД, migrations для поштучной; см. internal/db/migrate.go).
	migrationsDir := flag.String("dir", "", "Папка с файлами миграций (пусто — автоопределение)")
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
		dir := *migrationsDir
		if dir == "" {
			// По умолчанию новые файлы создаём в migrations/.
			dir = "migrations"
		}
		upPath, downPath, createErr := db.CreateMigrationFile(dir, *create)
		if createErr != nil {
			log.Fatal().Err(createErr).Msg("Не удалось создать файл миграции")
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

	// Применение миграций: канонический путь — RunMigrations (автодетект),
	// если -dir не задан явно. A-M5 (pass 34): один runner вместо двух.
	if *migrationsDir != "" {
		if err := db.MigrateFromDir(database, *migrationsDir); err != nil {
			log.Fatal().Err(err).Msg("Ошибка применения миграций")
		}
	} else {
		if err := db.RunMigrations(database); err != nil {
			log.Fatal().Err(err).Msg("Ошибка применения миграций")
		}
	}

	log.Info().Msg("Миграции успешно применены")
}
