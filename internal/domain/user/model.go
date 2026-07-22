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
	Password          string `gorm:"not null" json:"-"`
	Name              string `gorm:"not null"`
	Role              string `gorm:"default:user"` // user / admin
	EmailVerified     bool   `gorm:"default:false"`
	AvatarPath        string `gorm:"default:''"`
	ProfileVisibility string `gorm:"default:public"` // public / hidden
	// 2FA fields
	TwoFactorEnabled     bool               `gorm:"default:false"`               // включена ли 2FA
	TwoFactorSecret      string             `gorm:"default:'';size:32" json:"-"` // секрет для TOTP (Base32)
	TwoFactorBackupCodes string             `gorm:"default:''" json:"-"`         // резервные коды (через запятую, хешированные)
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
	UserID       uint   `gorm:"not null;index:idx_external_logins_user"`
	Provider     string `gorm:"not null;index:idx_external_logins_provider"` // для поиска по провайдеру
	ExternalID   string `gorm:"not null"`
	AccessToken  string `json:"-"`
	RefreshToken string `json:"-"`
	ExpiresAt    time.Time
}

// PasswordResetToken хранит токен для сброса пароля.
type PasswordResetToken struct {
	ID        uint       `gorm:"primaryKey"`
	UserID    uint       `gorm:"not null;index:idx_password_reset_user"`
	ResetCode string     `gorm:"uniqueIndex;not null"` // одноразовый код в URL сброса
	TokenHash string     `gorm:"uniqueIndex;not null"` // SHA256 хеш токена
	ExpiresAt time.Time  `gorm:"not null"`
	UsedAt    *time.Time `gorm:"index:idx_password_reset_used"`
}

// EmailVerificationToken хранит токен для подтверждения email.
type EmailVerificationToken struct {
	ID        uint      `gorm:"primaryKey"`
	UserID    uint      `gorm:"not null;uniqueIndex"`
	TokenHash string    `gorm:"uniqueIndex;not null"` // SHA256 хеш токена
	ExpiresAt time.Time `gorm:"not null"`
}

// PublicUser — безопасное представление пользователя для публичного API и шаблонов.
type PublicUser struct {
	ID                uint          `json:"id"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
	Email             string        `json:"email"`
	Name              string        `json:"name"`
	Role              string        `json:"role"`
	EmailVerified     bool          `json:"email_verified"`
	AvatarPath        string        `json:"avatar_path"`
	ProfileVisibility string        `json:"profile_visibility"`
	TwoFactorEnabled  bool          `json:"two_factor_enabled"`
	Achievements      []Achievement `json:"achievements,omitempty"`
}

func (u *User) ToPublic() PublicUser {
	return PublicUser{
		ID:                u.ID,
		CreatedAt:         u.CreatedAt,
		UpdatedAt:         u.UpdatedAt,
		Email:             u.Email,
		Name:              u.Name,
		Role:              u.Role,
		EmailVerified:     u.EmailVerified,
		AvatarPath:        u.AvatarPath,
		ProfileVisibility: u.ProfileVisibility,
		TwoFactorEnabled:  u.TwoFactorEnabled,
		Achievements:      u.Achievements,
	}
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
	UserID   uint   `gorm:"not null;index:idx_push_subscriptions_user"`
	Endpoint string `gorm:"not null;index:idx_push_subscriptions_endpoint"`
	Auth     string `gorm:"not null" json:"-"`
	P256dh   string `gorm:"not null" json:"-"`
}

// RefreshToken хранит информацию о выданных refresh-токенах для возможности отзыва.
type RefreshToken struct {
	ID        uint       `gorm:"primaryKey"`
	UserID    uint       `gorm:"not null;index:idx_refresh_tokens_user"`
	TokenHash string     `gorm:"uniqueIndex;not null" json:"-"`   // SHA256 хеш токена
	DeviceID  string     `gorm:"index:idx_refresh_tokens_device"` // опциональный идентификатор устройства
	ExpiresAt time.Time  `gorm:"not null"`
	RevokedAt *time.Time `gorm:"index:idx_refresh_tokens_revoked"` // NULL, если не отозван
	CreatedAt time.Time  `gorm:"autoCreateTime"`
}
