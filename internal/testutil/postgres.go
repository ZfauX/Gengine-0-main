// Package testutil содержит утилиты для тестирования с изолированными схемами PostgreSQL.
package testutil

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// testDSN возвращает DSN для подключения к тестовой PostgreSQL.
// Использует переменные окружения или fallback на значения по умолчанию.
func testDSN() string {
	host := os.Getenv("TEST_DB_HOST")
	if host == "" {
		host = "localhost"
	}
	port := os.Getenv("TEST_DB_PORT")
	if port == "" {
		port = "5432"
	}
	user := os.Getenv("TEST_DB_USER")
	if user == "" {
		user = "test"
	}
	password := os.Getenv("TEST_DB_PASSWORD")
	if password == "" {
		password = "test"
	}
	dbname := os.Getenv("TEST_DB_NAME")
	if dbname == "" {
		dbname = "gengine_test"
	}
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable", host, port, user, password, dbname)
}

// SetupPostgresDB создаёт изолированную схему в тестовой PostgreSQL,
// выполняет миграцию моделей и возвращает подключение к ней.
// После завершения теста схема автоматически удаляется.
// В случае ошибки вызывает t.Fatalf.
func SetupPostgresDB(t *testing.T, models ...any) *gorm.DB {
	t.Helper()

	// Уникальное имя схемы: test_<случайный hex>
	randomBytes := make([]byte, 8)
	if _, err := rand.Read(randomBytes); err != nil {
		t.Fatalf("Не удалось сгенерировать случайное имя схемы: %v", err)
	}
	schemaName := "test_" + hex.EncodeToString(randomBytes)

	// Основное подключение к базе (без указания схемы)
	dsn := testDSN()
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
			_ = sqlDB.Close()
		}
		// Создаём новое подключение для удаления схемы
		cleanupDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
		if err != nil {
			t.Logf("Не удалось подключиться для очистки схемы %s: %v", schemaName, err)
			return
		}
		defer func() {
			if sqlDB, err := cleanupDB.DB(); err == nil {
				_ = sqlDB.Close()
			}
		}()
		if err := cleanupDB.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schemaName)).Error; err != nil {
			t.Logf("Не удалось удалить схему %s: %v", schemaName, err)
		}
	})

	return db
}

// SetupPostgresDBOrSkip вызывает SetupPostgresDB, но если та завершается с паникой
// (например, из-за недоступности PostgreSQL), то перехватывает панику и пропускает тест.
// Это удобно для интеграционных тестов, которые не должны падать при отсутствии БД.
func SetupPostgresDBOrSkip(t *testing.T, models ...any) *gorm.DB {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Skipf("Skipping integration test: PostgreSQL setup failed: %v", r)
		}
	}()
	return SetupPostgresDB(t, models...)
}
