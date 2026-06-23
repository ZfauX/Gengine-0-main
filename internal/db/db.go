// Package db предоставляет подключение, миграции и начальное заполнение БД.
package db

import (
	"fmt"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/admin"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/monitor"
	"gengine-0/internal/domain/social"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/tournament"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/audit"
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

// Migrate выполняет автоматические миграции всех моделей.
func Migrate(db *gorm.DB) error {
	models := []any{
		&user.User{}, &user.Achievement{}, &user.ExternalLogin{}, &user.PasswordResetToken{},
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{}, &game.CoAuthor{}, &game.Note{},
		&game.LevelProgress{}, &game.Attempt{}, &game.Photo{},
		&level.Level{}, &level.Question{}, &level.Answer{},
		&team.Team{}, &team.Invitation{},
		&monitor.ChatRoom{}, &monitor.ChatMessage{}, &monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&social.PlayerRating{}, &social.Follow{},
		&game.Review{}, &game.PlayerRating{},
		&admin.AuditLog{}, &admin.Backup{}, &audit.Entry{},
		&tournament.Tournament{}, &tournament.TournamentGame{}, &tournament.TournamentTeam{}, &tournament.TournamentResult{},
	}
	return db.AutoMigrate(models...)
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
