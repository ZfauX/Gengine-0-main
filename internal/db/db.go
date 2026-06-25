// internal/db/db.go
package db

import (
	"fmt"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/logging"

	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Connect открывает и настраивает соединение с PostgreSQL.
func Connect(cfg *config.Config) (*gorm.DB, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		cfg.Database.Host, cfg.Database.Port,
		cfg.Database.User, cfg.Database.Password,
		cfg.Database.Name, cfg.Database.SSLMode,
	)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: &logging.GormLogger{LogLevel: logger.Info},
	})
	if err != nil {
		return nil, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.Database.ConnMaxLifetime)

	return db, nil
}

// EnsureAdmin создаёт или обновляет учётную запись администратора.
func EnsureAdmin(db *gorm.DB, cfg *config.Config) {
	hashed, err := bcrypt.GenerateFromPassword([]byte(cfg.Admin.Password), bcrypt.DefaultCost)
	if err != nil {
		log.Error().Err(err).Msg("ensureAdmin: не удалось захешировать пароль")
		return
	}

	var adminUser user.User
	result := db.Where("role = ?", "admin").First(&adminUser)
	if result.Error == nil {
		adminUser.Password = string(hashed)
		adminUser.Email = cfg.Admin.Email
		if err := db.Save(&adminUser).Error; err != nil {
			log.Error().Err(err).Msg("ensureAdmin: не удалось обновить администратора")
			return
		}
		log.Info().Str("email", adminUser.Email).Msg("Администратор обновлён")
		return
	}

	adminUser = user.User{
		Email:         cfg.Admin.Email,
		Password:      string(hashed),
		Name:          "Администратор",
		Role:          "admin",
		EmailVerified: true,
	}
	if err := db.Create(&adminUser).Error; err != nil {
		log.Error().Err(err).Msg("ensureAdmin: не удалось создать администратора")
		return
	}

	log.Info().Str("email", adminUser.Email).Msg("Создан администратор")
}
