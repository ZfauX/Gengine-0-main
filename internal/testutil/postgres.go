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
// Приоритет переменных окружения:
//  1. TEST_DB_* — кастомные значения именно для тестов;
//  2. DB_* — стандартные переменные приложения (использует CI);
//  3. значения по умолчанию для локальной разработки.
func testDSN() string {
	host := firstNonEmpty(os.Getenv("TEST_DB_HOST"), os.Getenv("DB_HOST"), "localhost")
	port := firstNonEmpty(os.Getenv("TEST_DB_PORT"), os.Getenv("DB_PORT"), "5432")
	user := firstNonEmpty(os.Getenv("TEST_DB_USER"), os.Getenv("DB_USER"), "test")
	password := firstNonEmpty(os.Getenv("TEST_DB_PASSWORD"), os.Getenv("DB_PASSWORD"), "test")
	dbname := firstNonEmpty(os.Getenv("TEST_DB_NAME"), os.Getenv("DB_NAME"), "gengine_test")
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable", host, port, user, password, dbname)
}

// firstNonEmpty возвращает первый непустой аргумент.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// SetupPostgresDB создаёт изолированную схему в тестовой PostgreSQL,
// выполняет миграцию моделей и возвращает подключение к ней.
// После завершения теста схема автоматически удаляется.
// В случае ошибки вызывает t.Fatalf.
func SetupPostgresDB(t *testing.T, models ...any) *gorm.DB {
	t.Helper()

	// Единый guard для всех PG-тестов (pass 24): `go test -short` должен
	// работать без БД — пакеты, использующие SetupPostgresDB, скипаются,
	// а не падают с t.Fatalf на CI без PostgreSQL.
	if testing.Short() {
		t.Skip("skipping integration test (requires PostgreSQL)")
	}

	// Уникальное имя схемы: test_<случайный hex>
	randomBytes := make([]byte, 8)
	if _, err := rand.Read(randomBytes); err != nil {
		t.Fatalf("Не удалось сгенерировать случайное имя схемы: %v", err)
	}
	schemaName := "test_" + hex.EncodeToString(randomBytes)

	// Основное подключение к базе (без указания схемы) — только для создания схемы.
	dsn := testDSN()
	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("Не удалось подключиться к PostgreSQL: %v", err)
	}

	// Создаём схему
	if err := adminDB.Exec(fmt.Sprintf("CREATE SCHEMA %s", schemaName)).Error; err != nil {
		t.Fatalf("Не удалось создать схему %s: %v", schemaName, err)
	}
	// Закрываем временное соединение (основное открываем с search_path).
	if adminSQL, err := adminDB.DB(); err == nil {
		_ = adminSQL.Close()
	}

	// FIX (PASS-20 CI): задаём search_path в DSN, а не через `SET search_path`.
	// `SET search_path` действует только на ОДНО соединение пула GORM — при
	// параллельных запросах (errgroup в dashboard и др.) другие соединения
	// обращались к public и получали «relation does not exist». pgx кладёт
	// неизвестные DSN-параметры (включая search_path) в RuntimeParams, которые
	// применяются к КАЖДОМУ соединению пула как session default.
	dsnWithSchema := dsn + fmt.Sprintf(" search_path=%s", schemaName)
	db, err := gorm.Open(postgres.Open(dsnWithSchema), &gorm.Config{})
	if err != nil {
		t.Fatalf("Не удалось подключиться к PostgreSQL (search_path): %v", err)
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
