// internal/db/migrate.go
package db

import (
	"errors"
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

// hasAppliedMigrations проверяет, есть ли уже применённые миграции в БД.
func hasAppliedMigrations(gdb *gorm.DB) bool {
	var count int64
	gdb.Raw("SELECT COUNT(*) FROM schema_migrations").Scan(&count)
	return count > 0
}

// RunMigrations запускает миграции. Для свежей БД использует squashed-файлы
// (migrations_squashed/), для существующей — обычные поштучные (migrations/).
func RunMigrations(gdb *gorm.DB) error {
	return MigrateFromDir(gdb, "")
}

// MigrateFromDir запускает миграции из указанной папки (или автоопределение,
// если dir пустой).
func MigrateFromDir(gdb *gorm.DB, migrationsDir string) error {
	sqlDB, err := gdb.DB()
	if err != nil {
		return fmt.Errorf("не удалось получить sql.DB: %w", err)
	}

	driver, err := postgres.WithInstance(sqlDB, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("не удалось создать драйвер миграции: %w", err)
	}

	if migrationsDir == "" {
		if hasAppliedMigrations(gdb) {
			migrationsDir = "migrations"
			log.Info().Msg("БД содержит миграции — применяем поштучные файлы")
		} else {
			migrationsDir = "migrations_squashed"
			log.Info().Msg("Свежая БД — применяем сгруппированные миграции")
		}
	}

	if _, statErr := os.Stat(migrationsDir); os.IsNotExist(statErr) {
		if mkdirErr := os.MkdirAll(migrationsDir, 0755); mkdirErr != nil {
			return fmt.Errorf("не удалось создать папку миграций: %w", mkdirErr)
		}
		// Если папка только что создана и пуста — используем миграции из individual-директории
		if migrationsDir == "migrations_squashed" {
			log.Warn().Msg("migrations_squashed не найдены, переключаемся на individual migrations/")
			migrationsDir = "migrations"
			// Повторная проверка existence
			if _, statErr2 := os.Stat(migrationsDir); os.IsNotExist(statErr2) {
				log.Warn().Str("dir", migrationsDir).Msg("Папки с миграциями нет, создаём пустую")
				if mkdirErr2 := os.MkdirAll(migrationsDir, 0755); mkdirErr2 != nil {
					return fmt.Errorf("не удалось создать папку миграций: %w", mkdirErr2)
				}
				return nil
			}
		} else {
			log.Warn().Str("dir", migrationsDir).Msg("Папка миграций создана, но файлы отсутствуют")
			return nil
		}
	}

	m, err := migrate.NewWithDatabaseInstance(
		"file://"+filepath.ToSlash(migrationsDir),
		"postgres", driver)
	if err != nil {
		return fmt.Errorf("не удалось создать экземпляр миграции: %w", err)
	}

	version, dirty, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return fmt.Errorf("ошибка получения версии миграций: %w", err)
	}
	if dirty {
		log.Warn().Uint("version", version).Msg("Миграции в грязном состоянии")
	}

	if upErr := m.Up(); upErr != nil && !errors.Is(upErr, migrate.ErrNoChange) {
		return fmt.Errorf("ошибка применения миграций: %w", upErr)
	}

	// Если применяли squashed, нужно также применить индивидуальные миграции,
	// которые вышли после создания squashed набора
	if migrationsDir == "migrations_squashed" {
		newVersion, _, _ := m.Version()
		log.Info().Uint("version", newVersion).Msg("Squashed миграции применены")

		// Закрываем текущий экземпляр миграции
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			log.Warn().Err(srcErr).Msg("migrate: close source error")
		}
		if dbErr != nil {
			log.Warn().Err(dbErr).Msg("migrate: close db error")
		}

		// Создаём новый экземпляр для индивидуальных миграций
		// golang-migrate пропустит уже применённые версии (1-5) и применит 6-20
		indivM, err := migrate.NewWithDatabaseInstance(
			"file://"+filepath.ToSlash("migrations"),
			"postgres", driver)
		if err != nil {
			return fmt.Errorf("не удалось создать экземпляр для индивидуальных миграций: %w", err)
		}

		if upErr := indivM.Up(); upErr != nil && !errors.Is(upErr, migrate.ErrNoChange) {
			return fmt.Errorf("ошибка применения индивидуальных миграций: %w", upErr)
		}

		newVersion, dirtyAfter, versionErr := indivM.Version()
		if versionErr == nil && dirtyAfter {
			log.Warn().Uint("version", newVersion).Msg("Индивидуальные миграции в грязном состоянии после применения")
		} else if versionErr == nil {
			log.Info().Uint("version", newVersion).Msg("Миграции успешно применены")
		} else {
			log.Info().Msg("Миграции успешно применены")
		}

		srcErr, dbErr = indivM.Close()
		if srcErr != nil {
			log.Warn().Err(srcErr).Msg("migrate: close source error")
		}
		if dbErr != nil {
			log.Warn().Err(dbErr).Msg("migrate: close db error")
		}
		return nil
	}

	newVersion, dirtyAfter, versionErr := m.Version()
	if versionErr == nil && dirtyAfter {
		log.Warn().Uint("version", newVersion).Msg("Миграции в грязном состоянии после применения")
	} else if versionErr == nil {
		log.Info().Uint("version", newVersion).Msg("Миграции успешно применены")
	} else {
		log.Info().Msg("Миграции успешно применены")
	}
	return nil
}

// CreateMigrationFile создаёт новый файл миграции с указанным именем.
func CreateMigrationFile(migrationsDir, name string) (upPath, downPath string, err error) {
	if err = os.MkdirAll(migrationsDir, 0755); err != nil {
		return "", "", err
	}

	timestamp := time.Now().Format("20060102150405")
	upPath = filepath.Join(migrationsDir, fmt.Sprintf("%s_%s.up.sql", timestamp, name))
	downPath = filepath.Join(migrationsDir, fmt.Sprintf("%s_%s.down.sql", timestamp, name))

	if err = os.WriteFile(upPath, []byte("-- "+name+" up\n"), 0644); err != nil {
		return "", "", err
	}
	if err = os.WriteFile(downPath, []byte("-- "+name+" down\n"), 0644); err != nil {
		return "", "", err
	}
	return upPath, downPath, nil
}
