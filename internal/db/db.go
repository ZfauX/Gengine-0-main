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

// EnsureAdmin создаёт учётную запись администратора, если её ещё нет.
// Использует учетные данные из cfg.Admin (Email и Password).
// Операция атомарна: INSERT с ON CONFLICT DO NOTHING исключает гонку.
func EnsureAdmin(db *gorm.DB, cfg *config.Config) error {
	var existing user.User
	if err := db.Where("email = ?", cfg.Admin.Email).First(&existing).Error; err == nil {
		log.Info().Str("email", cfg.Admin.Email).Msg("Администратор уже существует, пропускаем создание")
		return nil
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(cfg.Admin.Password), crypto.BcryptCost)
	if err != nil {
		return fmt.Errorf("ensureAdmin: не удалось захешировать пароль администратора: %w", err)
	}

	admin := user.User{
		Email:         cfg.Admin.Email,
		Password:      string(hashed),
		Name:          "Администратор",
		Role:          "admin",
		EmailVerified: true,
	}

	result := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&admin)
	if result.Error != nil {
		return fmt.Errorf("ensureAdmin: не удалось создать администратора: %w", result.Error)
	}

	if result.RowsAffected > 0 {
		log.Info().Str("email", admin.Email).Msg("Администратор создан")
	} else {
		log.Info().Str("email", cfg.Admin.Email).Msg("Администратор уже существует, пропускаем создание")
	}
	return nil
}
