// internal/db/db.go
package db

import (
	"fmt"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/crypto"
	"gengine-0/internal/pkg/logging"

	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// Connect устанавливает соединение с PostgreSQL на основе переданной конфигурации.
// Параметры подключения формируются из полей cfg.Database (Host, Port, User, Password, Name, SSLMode).
// После подключения настраиваются параметры пула соединений:
//   - MaxOpenConns
//   - MaxIdleConns
//   - ConnMaxLifetime
//   - ConnMaxIdleTime (добавлено)
//
// Значения берутся из cfg.Database.
// Возвращает указатель на gorm.DB и ошибку, если соединение не удалось установить.
// Для логирования используется кастомный GormLogger из пакета logging.
func Connect(cfg *config.Config) (*gorm.DB, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		cfg.Database.Host, cfg.Database.Port,
		cfg.Database.User, cfg.Database.Password,
		cfg.Database.Name, cfg.Database.SSLMode,
	)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: &logging.GormLogger{LogLevel: logger.Warn},
	})
	if err != nil {
		return nil, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	// Настройка пула соединений
	sqlDB.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.Database.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(cfg.Database.ConnMaxIdleTime)

	// Логируем настройки пула для диагностики
	log.Info().
		Int("max_open_conns", cfg.Database.MaxOpenConns).
		Int("max_idle_conns", cfg.Database.MaxIdleConns).
		Dur("conn_max_lifetime", cfg.Database.ConnMaxLifetime).
		Dur("conn_max_idle_time", cfg.Database.ConnMaxIdleTime).
		Msg("Настройки пула соединений БД")

	return db, nil
}

// EnsureAdmin создаёт или обновляет учётную запись администратора в базе данных.
// Использует учетные данные из cfg.Admin (Email и Password).
// Алгоритм:
//  1. Хеширует пароль с помощью bcrypt со стоимостью 12 (рекомендовано для продакшена).
//  2. Ищет пользователя с email = cfg.Admin.Email.
//     - Если найден, обновляет его пароль и устанавливает роль admin (если ещё не admin).
//     - Если не найден, создаёт нового пользователя с ролью admin.
//  3. В случае любой ошибки возвращает её, чтобы вызывающий код мог обработать.
//
// Возвращает ошибку, если не удалось выполнить операцию.
// Вызывающий код должен проверить ошибку и завершить приложение, если это критично.
// EnsureAdmin создаёт или обновляет учётную запись администратора.
func EnsureAdmin(db *gorm.DB, cfg *config.Config) error {
	hashed, err := bcrypt.GenerateFromPassword([]byte(cfg.Admin.Password), crypto.BcryptCost)
	if err != nil {
		return fmt.Errorf("ensureAdmin: не удалось захешировать пароль администратора: %w", err)
	}

	// Используем OnConflict для идемпотентности (UPSERT)
	admin := user.User{
		Email:         cfg.Admin.Email,
		Password:      string(hashed),
		Name:          "Администратор",
		Role:          "admin",
		EmailVerified: true,
	}
	if err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "email"}},
		DoUpdates: clause.AssignmentColumns([]string{"password", "role", "email_verified"}),
	}).Create(&admin).Error; err != nil {
		return fmt.Errorf("ensureAdmin: не удалось создать/обновить администратора: %w", err)
	}

	log.Info().Str("email", admin.Email).Msg("Администратор создан/обновлён")
	return nil
}
