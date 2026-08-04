// internal/domain/user/model.go
package user

import (
	"encoding/json"
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
	Plan              string `gorm:"default:'free'"`
	StripeCustomerID  string `gorm:"default:''"`
	// Настройки темы (JSON) — автосмена + время тёмной темы.
	ThemeSettingsJSON string `gorm:"column:theme_settings;type:text;default:''" json:"-"`
	// Вид списка игр (table | cards) — серверная персонализация.
	GamesView string `gorm:"column:games_view;type:varchar(10);default:'table'" json:"-"`
	// 2FA fields
	TwoFactorEnabled     bool               `gorm:"default:false"`               // включена ли 2FA
	TwoFactorSecret      string             `gorm:"default:'';size:64" json:"-"` // секрет для TOTP (Base32)
	TwoFactorBackupCodes string             `gorm:"default:''" json:"-"`         // резервные коды (через запятую, хешированные)
	FailedLoginAttempts  int                `gorm:"default:0"`                   // количество неудачных попыток входа
	LockedUntil          *time.Time         `gorm:"index"`                       // блокировка до указанного времени
	Achievements         []Achievement      `gorm:"many2many:user_achievements;"`
	ExternalLogins       []ExternalLogin    `gorm:"foreignKey:UserID"`
	Subscriptions        []PushSubscription `gorm:"foreignKey:UserID"`
	RefreshTokens        []RefreshToken     `gorm:"foreignKey:UserID"` // добавлено
}

// ThemeSettings хранит настройки автоматической смены темы пользователя.
// Хранится как JSON в колонке users.theme_settings.
type ThemeSettings struct {
	AutoTheme bool   `json:"auto_theme"` // включена ли автоматическая смена темы
	DarkFrom  string `json:"dark_from"`  // начало тёмной темы, формат "HH:MM" (например "20:00")
	DarkTo    string `json:"dark_to"`    // конец тёмной темы, формат "HH:MM" (например "07:00")
}

// DefaultThemeSettings возвращает настройки темы по умолчанию.
func DefaultThemeSettings() ThemeSettings {
	return ThemeSettings{
		AutoTheme: true,
		DarkFrom:  "20:00",
		DarkTo:    "07:00",
	}
}

// ParseThemeSettings разбирает JSON из БД; пустая строка → значения по умолчанию.
func ParseThemeSettings(jsonStr string) (ThemeSettings, error) {
	if jsonStr == "" {
		return DefaultThemeSettings(), nil
	}
	var ts ThemeSettings
	if err := json.Unmarshal([]byte(jsonStr), &ts); err != nil {
		return DefaultThemeSettings(), err
	}
	return ts, nil
}

// MarshalThemeSettings сериализует настройки темы в JSON для БД.
func MarshalThemeSettings(ts ThemeSettings) (string, error) {
	b, err := json.Marshal(ts)
	if err != nil {
		return "", err
	}
	return string(b), nil
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
	ID        uint           `gorm:"primaryKey"`
	UserID    uint           `gorm:"not null;index:idx_password_reset_user"`
	ResetCode string         `gorm:"uniqueIndex;not null"` // одноразовый код в URL сброса
	TokenHash string         `gorm:"uniqueIndex;not null"` // SHA256 хеш токена
	ExpiresAt time.Time      `gorm:"not null"`
	UsedAt    *time.Time     `gorm:"index:idx_password_reset_used"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime"`
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

// EmailVerificationToken хранит токен для подтверждения email.
type EmailVerificationToken struct {
	ID               uint           `gorm:"primaryKey"`
	UserID           uint           `gorm:"not null;index"`
	TokenHash        string         `gorm:"uniqueIndex;not null"`        // SHA256 хеш токена
	VerificationCode string         `gorm:"uniqueIndex;not null;size:8"` // короткий код для URL (8 символов)
	ExpiresAt        time.Time      `gorm:"not null"`
	UpdatedAt        time.Time      `gorm:"autoUpdateTime"`
	DeletedAt        gorm.DeletedAt `gorm:"index"`
}

// PublicUser — безопасное представление пользователя для публичного API и шаблонов.
// Email намеренно исключён — приватность пользователя (не сериализуется).
type PublicUser struct {
	ID                uint          `json:"id"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
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
	ID           uint           `gorm:"primaryKey"`
	UserID       uint           `gorm:"uniqueIndex;not null"`
	SettingsJSON string         `gorm:"type:text"`
	UpdatedAt    time.Time      `gorm:"autoUpdateTime"`
	DeletedAt    gorm.DeletedAt `gorm:"index"`
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
	ID                uint           `gorm:"primaryKey"`
	UserID            uint           `gorm:"not null;index:idx_refresh_tokens_user"`
	TokenHash         string         `gorm:"uniqueIndex;not null" json:"-"`   // SHA256 хеш токена
	DeviceID          string         `gorm:"index:idx_refresh_tokens_device"` // опциональный идентификатор устройства
	ClientFingerprint string         `gorm:"default:''" json:"-"`             // SHA256(UserAgent + IP prefix) для token binding
	ExpiresAt         time.Time      `gorm:"not null"`
	RevokedAt         *time.Time     `gorm:"index:idx_refresh_tokens_revoked"` // NULL, если не отозван
	CreatedAt         time.Time      `gorm:"autoCreateTime"`
	UpdatedAt         time.Time      `gorm:"autoUpdateTime"`
	DeletedAt         gorm.DeletedAt `gorm:"index"`
}
