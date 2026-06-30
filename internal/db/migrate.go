// internal/db/migrate.go
package db

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// MigrateFromFiles выполняет миграции из файлов в папке ./migrations.
// Использует golang-migrate для версионирования, что гарантирует идемпотентность
// (каждая миграция применяется только один раз).
func MigrateFromFiles(db *gorm.DB, migrationsDir string) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("не удалось получить sql.DB: %w", err)
	}

	driver, err := postgres.WithInstance(sqlDB, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("не удалось создать драйвер миграции: %w", err)
	}

	// Проверяем существование папки с миграциями
	if _, err := os.Stat(migrationsDir); os.IsNotExist(err) {
		if err := os.MkdirAll(migrationsDir, 0755); err != nil {
			return fmt.Errorf("не удалось создать папку миграций: %w", err)
		}
		log.Warn().Str("dir", migrationsDir).Msg("Папка миграций создана, но файлы отсутствуют. Создайте их вручную.")
		return nil
	}

	m, err := migrate.NewWithDatabaseInstance(
		"file://"+filepath.ToSlash(migrationsDir),
		"postgres", driver)
	if err != nil {
		return fmt.Errorf("не удалось создать экземпляр миграции: %w", err)
	}

	// Получаем текущую версию
	version, dirty, err := m.Version()
	if err != nil && err != migrate.ErrNilVersion {
		return fmt.Errorf("ошибка получения версии миграций: %w", err)
	}
	if dirty {
		log.Warn().Uint("version", version).Msg("Миграции в грязном состоянии. Попытка принудительного применения...")
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("ошибка применения миграций: %w", err)
	}

	newVersion, _, _ := m.Version()
	log.Info().Uint("version", newVersion).Msg("Миграции успешно применены")
	return nil
}

// RunMigrations — обёртка для MigrateFromFiles с более простым интерфейсом.
func RunMigrations(db *gorm.DB, migrationsDir string) error {
	return MigrateFromFiles(db, migrationsDir)
}

// CreateMigrationFile создаёт новый файл миграции с указанным именем.
func CreateMigrationFile(migrationsDir, name string) (upPath, downPath string, err error) {
	if err := os.MkdirAll(migrationsDir, 0755); err != nil {
		return "", "", err
	}

	timestamp := time.Now().Format("20060102150405")
	upPath = filepath.Join(migrationsDir, fmt.Sprintf("%s_%s.up.sql", timestamp, name))
	downPath = filepath.Join(migrationsDir, fmt.Sprintf("%s_%s.down.sql", timestamp, name))

	if err := os.WriteFile(upPath, []byte("-- "+name+" up\n"), 0644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(downPath, []byte("-- "+name+" down\n"), 0644); err != nil {
		return "", "", err
	}
	return upPath, downPath, nil
}
