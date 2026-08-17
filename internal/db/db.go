// internal/db/db.go
package db

import (
	"fmt"
	"net"
	"net/url"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/crypto"
	"gengine-0/internal/pkg/logging"

	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
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
	// L1 (PASS-22): DSN через url.URL — пароль с спецсимволами (@, :, /, #)
	// раньше ломал key=value DSN (fmt.Sprintf без экранирования). UserPassword
	// делает процентное кодирование, pgx разбирает URL-формат.
	u := &url.URL{
		Scheme: "postgres",
		Host:   net.JoinHostPort(cfg.Database.Host, cfg.Database.Port),
		Path:   cfg.Database.Name,
		User:   url.UserPassword(cfg.Database.User, cfg.Database.Password),
	}
	q := u.Query()
	q.Set("sslmode", cfg.Database.SSLMode)
	u.RawQuery = q.Encode()
	dsn := u.String()

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: &logging.GormLogger{LogLevel: logger.Warn},
		// Не оборачивать каждую single-row операцию в BEGIN/COMMIT — явные
		// db.Transaction(...) уже используются там, где нужна атомарность.
		SkipDefaultTransaction: true,
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
// L12 (PASS-17): раньше bcrypt пересчитывался (~100-300мс) и пароль
// перезаписывался на КАЖДОМ старте (ON CONFLICT DO UPDATE). Теперь — только
// при создании: существующий админ не трогается (пароль из env не затирает
// сменённый пользователем).
func EnsureAdmin(db *gorm.DB, cfg *config.Config) error {
	var count int64
	if err := db.Model(&user.User{}).Where("email = ?", cfg.Admin.Email).Count(&count).Error; err != nil {
		return fmt.Errorf("ensureAdmin: не удалось проверить администратора: %w", err)
	}
	if count > 0 {
		log.Info().Str("email", cfg.Admin.Email).Msg("Администратор уже существует")
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

	if err := db.Create(&admin).Error; err != nil {
		return fmt.Errorf("ensureAdmin: не удалось создать администратора: %w", err)
	}
	log.Info().Str("email", admin.Email).Msg("Администратор создан")
	return nil
}
