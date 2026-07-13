// internal/domain/user/model.go
package user

import (
	"time"

	"gorm.io/gorm"
)

// User представляет пользователя платформы.
type User struct {
	gorm.Model
	Email             string `gorm:"uniqueIndex;not null"`
	Password          string `gorm:"not null"`
	Name              string `gorm:"not null"`
	Role              string `gorm:"default:user"` // user / admin
	EmailVerified     bool   `gorm:"default:false"`
	AvatarPath        string `gorm:"default:''"`
	ProfileVisibility string `gorm:"default:public"` // public / hidden
	Plan              string `gorm:"default:free"`   // free / basic / pro
	StripeCustomerID  string `gorm:"default:''"`
	// 2FA fields
	TwoFactorEnabled     bool               `gorm:"default:false"`      // включена ли 2FA
	TwoFactorSecret      string             `gorm:"default:'';size:32"` // секрет для TOTP (Base32)
	TwoFactorBackupCodes string             `gorm:"default:''"`         // резервные коды (через запятую, хешированные)
	Achievements         []Achievement      `gorm:"many2many:user_achievements;"`
	ExternalLogins       []ExternalLogin    `gorm:"foreignKey:UserID"`
	Subscriptions        []PushSubscription `gorm:"foreignKey:UserID"`
	RefreshTokens        []RefreshToken     `gorm:"foreignKey:UserID"` // добавлено
}

// Achievement представляет достижение (ачивку).
type Achievement struct {
	gorm.Model
	Code        string `gorm:"uniqueIndex;not null"`
	Name        string `gorm:"not null"`
	Description string
	Icon        string
	Users       []User `gorm:"many2many:user_achievements;"`
}

// ExternalLogin хранит привязку OAuth-аккаунта.
type ExternalLogin struct {
	gorm.Model
	UserID       uint   `gorm:"not null;index"`
	Provider     string `gorm:"not null"` // google, github, yandex
	ExternalID   string `gorm:"not null"`
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

// PasswordResetToken хранит токен для сброса пароля.
type PasswordResetToken struct {
	ID        uint      `gorm:"primaryKey"`
	UserID    uint      `gorm:"not null;index"`
	TokenHash string    `gorm:"uniqueIndex;not null"` // SHA256 хеш токена
	ExpiresAt time.Time `gorm:"not null"`
}

// EmailVerificationToken хранит токен для подтверждения email.
type EmailVerificationToken struct {
	ID        uint      `gorm:"primaryKey"`
	UserID    uint      `gorm:"not null;uniqueIndex"`
	TokenHash string    `gorm:"uniqueIndex;not null"` // SHA256 хеш токена
	ExpiresAt time.Time `gorm:"not null"`
}

// NotificationSetting хранит настройки уведомлений пользователя.
type NotificationSetting struct {
	ID     uint `gorm:"primaryKey"`
	UserID uint `gorm:"uniqueIndex;not null"`
	// JSON-строка с флагами каналов по типам событий
	SettingsJSON string `gorm:"type:text"`
}

// PushSubscription хранит подписку на push-уведомления.
type PushSubscription struct {
	gorm.Model
	UserID   uint   `gorm:"not null;index"`
	Endpoint string `gorm:"not null"`
	Auth     string `gorm:"not null"`
	P256dh   string `gorm:"not null"`
}

// RefreshToken хранит информацию о выданных refresh-токенах для возможности отзыва.
type RefreshToken struct {
	ID        uint       `gorm:"primaryKey"`
	UserID    uint       `gorm:"not null;index"`
	TokenHash string     `gorm:"uniqueIndex;not null"` // SHA256 хеш токена
	DeviceID  string     `gorm:"index"`                // опциональный идентификатор устройства
	ExpiresAt time.Time  `gorm:"not null"`
	RevokedAt *time.Time `gorm:"index"` // NULL, если не отозван
	CreatedAt time.Time  `gorm:"autoCreateTime"`
}
