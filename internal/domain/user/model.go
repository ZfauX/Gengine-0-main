// internal/domain/user/model.go
package user

import (
	"time"

	"gorm.io/gorm"
)

// User представляет пользователя платформы.
type User struct {
	gorm.Model
	Email             string    `gorm:"uniqueIndex;not null"`
	Password          string    `gorm:"not null"`
	Name              string    `gorm:"not null"`
	Role              string    `gorm:"default:user"` // user / admin
	EmailVerified     bool      `gorm:"default:false"`
	AvatarPath        string    `gorm:"default:''"`
	ProfileVisibility string    `gorm:"default:public"` // public / hidden
	Plan              string    `gorm:"default:free"`   // free / basic / pro
	StripeCustomerID  string    `gorm:"default:''"`
	Achievements      []Achievement     `gorm:"many2many:user_achievements;"`
	ExternalLogins    []ExternalLogin   `gorm:"foreignKey:UserID"`
	Subscriptions     []PushSubscription `gorm:"foreignKey:UserID"`
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
	Token     string    `gorm:"uniqueIndex;not null"`
	ExpiresAt time.Time `gorm:"not null"`
}

// EmailVerificationToken хранит токен для подтверждения email.
type EmailVerificationToken struct {
	ID        uint      `gorm:"primaryKey"`
	UserID    uint      `gorm:"not null;uniqueIndex"`
	Token     string    `gorm:"uniqueIndex;not null"`
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
	UserID  uint   `gorm:"not null;index"`
	Endpoint string `gorm:"not null"`
	Auth    string `gorm:"not null"`
	P256dh  string `gorm:"not null"`
}