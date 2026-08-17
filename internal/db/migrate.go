// internal/db/migrate.go
package db

import (
	"context"
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

// migrationLockTimeout — максимальное время ожидания advisory lock на миграции
// (MULTI-INSTANCE, PASS-12): если другой инстанс мигрирует дольше, стартующий
// инстанс падает с явной ошибкой (не бесконечно ждёт).
const migrationLockTimeout = 10 * time.Minute

// hasAppliedMigrations проверяет, есть ли уже применённые миграции в БД.
func hasAppliedMigrations(gdb *gorm.DB) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var count int64
	if err := gdb.WithContext(ctx).Raw("SELECT COUNT(*) FROM schema_migrations").Scan(&count).Error; err != nil {
		log.Warn().Err(err).Msg("hasAppliedMigrations: failed to count schema_migrations")
		return false
	}
	return count > 0
}

// getCurrentVersion возвращает текущую версию миграций из schema_migrations.
// DEEP-REVIEW MEDIUM #20 (pass 46): при ошибке БД возвращаем ошибку, а не 0 —
// раньше 0 заставлял MigrateFromDir выбрать squashed-набор для существующей
// индивидуальной БД при транзиентном сбое запроса.
func getCurrentVersion(gdb *gorm.DB) (uint, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var version int
	if err := gdb.WithContext(ctx).Raw("SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1").Scan(&version).Error; err != nil {
		return 0, fmt.Errorf("getCurrentVersion: failed to query schema_migrations: %w", err)
	}
	return uint(version), nil
}

// RunMigrations — канонический runner миграций (A-M5, pass 34). Для свежей БД
// использует squashed-файлы (migrations_squashed/), для существующей — обычные
// поштучные (migrations/). cmd/server/main.go и cmd/migrate/main.go (без -dir)
// вызывают именно его; MigrateFromDir с явной директорией — только для ручного
// применения конкретной папки (cmd/migrate -dir).
func RunMigrations(gdb *gorm.DB) error {
	return MigrateFromDir(gdb, "")
}

// cleanupGameSettings удаляет дубликаты game_settings, возникшие из-за
// пересечения soft-delete и уникального индекса по game_id в GORM.
func cleanupGameSettings(gdb *gorm.DB) {
	result := gdb.Exec("DELETE FROM game_settings WHERE id NOT IN (SELECT MIN(id) FROM game_settings GROUP BY game_id)")
	if result.Error != nil {
		log.Warn().Err(result.Error).Msg("cleanupGameSettings: failed to remove duplicates")
	} else if result.RowsAffected > 0 {
		log.Info().Int64("removed", result.RowsAffected).Msg("cleanupGameSettings: removed duplicate game_settings rows")
	}
}

// MigrateFromDir запускает миграции из указанной папки (или автоопределение,
// если dir пустой).
//
// MULTI-INSTANCE (PASS-12): берёт PostgreSQL advisory lock на время миграций,
// чтобы при одновременном старте N инстансов приложение не применяло миграции
// параллельно (гонка: один инстанс создаёт таблицу, второй падает на "already
// exists"). Инстансы, не получившие lock, ждут — после завершения мигрировавшего
// они увидят уже применённую схему (m.Up() вернёт ErrNoChange).
func MigrateFromDir(gdb *gorm.DB, migrationsDir string) error {
	sqlDB, err := gdb.DB()
	if err != nil {
		return fmt.Errorf("не удалось получить sql.DB: %w", err)
	}

	// Advisory lock (MULTI-INSTANCE, PASS-12). Используем отдельное соединение,
	// чтобы lock не был привязан к соединению из пула (которое может закрыться).
	// Фиксированный ключ (5123456) — один и тот же для всех инстансов.
	const migrationLockKey = 5123456
	lockConn, err := sqlDB.Conn(context.Background())
	if err != nil {
		return fmt.Errorf("не удалось получить соединение для migration lock: %w", err)
	}
	defer func() { _ = lockConn.Close() }()

	lockCtx, lockCancel := context.WithTimeout(context.Background(), migrationLockTimeout)
	defer lockCancel()
	if _, lockErr := lockConn.ExecContext(lockCtx, "SELECT pg_advisory_lock($1)", migrationLockKey); lockErr != nil {
		return fmt.Errorf("не удалось взять advisory lock на миграции: %w", lockErr)
	}
	// Освобождаем lock в любом случае (успех или ошибка).
	defer func() {
		unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer unlockCancel()
		if _, uErr := lockConn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1)", migrationLockKey); uErr != nil {
			log.Warn().Err(uErr).Msg("MigrateFromDir: failed to release advisory lock")
		}
	}()
	log.Info().Msg("MigrateFromDir: advisory lock acquired")

	// MultiStatementEnabled НЕ включаем (PASS-14): golang-migrate разбивает
	// файл по ";" и ломает dollar-quoted функции ($$...$$ с ";" внутри,
	// например миграция 000011 games_search_vector). Вместо этого миграции
	// 44-51 были переведены с CREATE INDEX CONCURRENTLY на обычные CREATE
	// INDEX — pgx/ExecContext выполняет весь файл одним statement корректно.
	driver, err := postgres.WithInstance(sqlDB, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("не удалось создать драйвер миграции: %w", err)
	}

	// maxSquashedVersion — последняя версия в squashed-наборе. Squashed —
	// «слепок» базовой схемы + tail (000009), покрывающий 000024-000062.
	// БД с версией <= maxSquashedVersion создана через squashed и продолжает
	// применяться squashed-набором (новые tail-версии догоняют схему).
	// БД с большей версией создана через индивидуальные миграции (migrations/).
	const maxSquashedVersion = 9

	if migrationsDir == "" {
		if hasAppliedMigrations(gdb) {
			currentVersion, verErr := getCurrentVersion(gdb)
			if verErr != nil {
				// DEEP-REVIEW MEDIUM #20 (pass 46): не угадываем директорию при
				// сбое БД — возвращаем ошибку (раньше 0 → squashed для инд. БД).
				return fmt.Errorf("не удалось определить текущую версию миграций: %w", verErr)
			}
			if currentVersion <= maxSquashedVersion {
				migrationsDir = "migrations_squashed"
				log.Info().Uint("version", currentVersion).Msg("БД из squashed-набора — применяем сгруппированные миграции")
			} else {
				migrationsDir = "migrations"
				log.Info().Msg("БД содержит миграции — применяем поштучные файлы")
			}
		} else {
			// Свежая БД. Раньше применялся squashed-набор, но он оказался
			// НЕСАМОДОСТАТОЧНЫМ (PASS-14): файлы ссылаются на таблицы из других
			// squashed-файлов в неверном порядке (например 000007_schema_tail
			// ссылается на notifications, созданные позже) → миграции падали
			// в dirty на свежей БД (docker/podman). Поштучные миграции (migrations/)
			// полностью работоспособны — применяем их с нуля.
			migrationsDir = "migrations"
			log.Info().Msg("Свежая БД — применяем поштучные миграции (squashed-набор несамодостаточен)")
		}
	}

	if _, statErr := os.Stat(migrationsDir); os.IsNotExist(statErr) {
		// M1 (PASS-22): НЕ создаём папку перед ошибкой — раньше MkdirAll создавал
		// пустую "migrations", и второй запуск из той же CWD не видел IsNotExist →
		// молча применял 0 миграций (старт на немигрированной схеме).
		if migrationsDir == "migrations_squashed" {
			log.Warn().Msg("migrations_squashed не найдены, переключаемся на individual migrations/")
			migrationsDir = "migrations"
			// Повторная проверка existence (без создания)
			if _, statErr2 := os.Stat(migrationsDir); os.IsNotExist(statErr2) {
				abs, _ := filepath.Abs(migrationsDir)
				return fmt.Errorf("папка миграций не найдена: %s (проверьте рабочую директорию — запустите сервер из корня проекта или укажите MIGRATIONS_DIR)", abs)
			}
		} else {
			abs, _ := filepath.Abs(migrationsDir)
			return fmt.Errorf("папка миграций не найдена: %s (запустите сервер из корня проекта)", abs)
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

	newVersion, dirtyAfter, versionErr := m.Version()
	if versionErr == nil && dirtyAfter {
		log.Warn().Uint("version", newVersion).Msg("Миграции в грязном состоянии после применения")
	} else if versionErr == nil {
		log.Info().Uint("version", newVersion).Msg("Миграции успешно применены")
	} else {
		log.Info().Msg("Миграции успешно применены")
	}

	// Clean up duplicate game_settings rows that can occur with GORM soft-delete + unique index
	cleanupGameSettings(gdb)

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
