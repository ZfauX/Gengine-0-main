// internal/testutil/postgres.go
package testutil

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// SetupPostgresDB создаёт изолированную схему в тестовой PostgreSQL,
// выполняет миграцию моделей и возвращает подключение к ней.
// После завершения теста схема автоматически удаляется.
func SetupPostgresDB(t *testing.T, models ...interface{}) *gorm.DB {
	t.Helper()

	// Уникальное имя схемы: test_<случайный hex>
	randomBytes := make([]byte, 8)
	if _, err := rand.Read(randomBytes); err != nil {
		t.Fatalf("Не удалось сгенерировать случайное имя схемы: %v", err)
	}
	schemaName := "test_" + hex.EncodeToString(randomBytes)

	// Основное подключение к базе (без указания схемы)
	dsn := "host=localhost port=5432 user=test password=test dbname=gengine_test sslmode=disable"
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("Не удалось подключиться к PostgreSQL: %v", err)
	}

	// Создаём схему
	if err := db.Exec(fmt.Sprintf("CREATE SCHEMA %s", schemaName)).Error; err != nil {
		t.Fatalf("Не удалось создать схему %s: %v", schemaName, err)
	}

	// Устанавливаем search_path для нашего подключения
	if err := db.Exec(fmt.Sprintf("SET search_path TO %s", schemaName)).Error; err != nil {
		t.Fatalf("Не удалось установить search_path: %v", err)
	}

	// Миграция моделей в этой схеме
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("Миграция в схеме %s не удалась: %v", schemaName, err)
	}

	// Автоочистка после теста
	t.Cleanup(func() {
		// Закрываем текущее соединение, чтобы не мешать удалению схемы
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
		// Создаём новое подключение для удаления схемы
		cleanupDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
		if err != nil {
			t.Logf("Не удалось подключиться для очистки схемы %s: %v", schemaName, err)
			return
		}
		defer func() {
			if sqlDB, err := cleanupDB.DB(); err == nil {
				sqlDB.Close()
			}
		}()
		if err := cleanupDB.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schemaName)).Error; err != nil {
			t.Logf("Не удалось удалить схему %s: %v", schemaName, err)
		}
	})

	return db
}