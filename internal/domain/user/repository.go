// Package user — repository interfaces for user domain.
package user

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"gorm.io/gorm"
)

// UserRepository определяет контракт для работы с пользователями.
type UserRepository interface {
	Create(ctx context.Context, user *User) error
	GetByID(ctx context.Context, id uint) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	GetPublicProfile(ctx context.Context, id uint) (*User, error)
	Update(ctx context.Context, id uint, fields map[string]any) error
	GetByRole(ctx context.Context, role string) ([]User, error)
	GetUserRole(ctx context.Context, id uint) (string, error)

	// Методы для админки с пагинацией
	Count(ctx context.Context) (int64, error)
	CountByRole(ctx context.Context, role string) (int64, error)
	List(ctx context.Context, role string) ([]User, error)
	ListPaginated(ctx context.Context, role string, offset, limit int) ([]User, error)
	Delete(ctx context.Context, id uint) error

	// AtomicIncrementFailedAttempts атомарно инкрементирует failed_login_attempts
	// и возвращает новое значение.
	AtomicIncrementFailedAttempts(ctx context.Context, userID uint) (int, error)
}

// AchievementRepository определяет контракт для работы с достижениями.
type AchievementRepository interface {
	Award(ctx context.Context, userID uint, achievement *Achievement) error
	GetByUserID(ctx context.Context, userID uint) ([]Achievement, error)
	Seed(ctx context.Context) error
	FirstOrCreate(ctx context.Context, achievement *Achievement) error
}

// PasswordResetRepository — контракт для сброса пароля.
type PasswordResetRepository interface {
	CreateToken(ctx context.Context, token *PasswordResetToken) error
	GetToken(ctx context.Context, tokenStr string) (*PasswordResetToken, error)
	GetTokenByResetCode(ctx context.Context, code string) (*PasswordResetToken, error)
	DeleteToken(ctx context.Context, token *PasswordResetToken) error
	MarkTokenUsed(ctx context.Context, id uint, usedAt time.Time) error
}

// EmailVerificationRepository — контракт для верификации email.
type EmailVerificationRepository interface {
	CreateToken(ctx context.Context, token *EmailVerificationToken) error
	GetToken(ctx context.Context, tokenStr string) (*EmailVerificationToken, error)
	GetTokenByCode(ctx context.Context, code string) (*EmailVerificationToken, error)
	DeleteToken(ctx context.Context, token *EmailVerificationToken) error
	DeleteByTokenHash(ctx context.Context, tokenHash string) error
	DeleteByUserID(ctx context.Context, userID uint) error
}

// ExternalLoginRepository — контракт для OAuth-привязок.
type ExternalLoginRepository interface {
	FindOrCreate(ctx context.Context, login *ExternalLogin) error
}

// RefreshTokenRepository — контракт для работы с refresh-токенами (добавлен).
type RefreshTokenRepository interface {
	Create(ctx context.Context, token *RefreshToken) error
	GetByTokenHash(ctx context.Context, tokenHash string) (*RefreshToken, error)
	Revoke(ctx context.Context, id uint) error
	RevokeAllForUser(ctx context.Context, userID uint) error
	DeleteExpired(ctx context.Context) error
}

// ---------- GORM implementations ----------

type gormUserRepo struct{ db *gorm.DB }

func NewGormUserRepo(db *gorm.DB) UserRepository { return &gormUserRepo{db} }

func (r *gormUserRepo) Create(ctx context.Context, user *User) error {
	return r.db.WithContext(ctx).Create(user).Error
}
func (r *gormUserRepo) GetByID(ctx context.Context, id uint) (*User, error) {
	var u User
	err := r.db.WithContext(ctx).First(&u, id).Error
	if err != nil {
		return nil, err
	}
	return &u, nil
}
func (r *gormUserRepo) GetByEmail(ctx context.Context, email string) (*User, error) {
	var u User
	err := r.db.WithContext(ctx).Where("email = ?", email).First(&u).Error
	if err != nil {
		return nil, err
	}
	return &u, nil
}
func (r *gormUserRepo) GetPublicProfile(ctx context.Context, id uint) (*User, error) {
	var u User
	err := r.db.WithContext(ctx).Preload("Achievements").First(&u, id).Error
	if err != nil {
		return nil, err
	}
	return &u, nil
}
func (r *gormUserRepo) Update(ctx context.Context, id uint, fields map[string]any) error {
	return r.db.WithContext(ctx).Model(&User{}).Where("id = ?", id).Updates(fields).Error
}

// GetByRole returns multiple users by role.
func (r *gormUserRepo) GetByRole(ctx context.Context, role string) ([]User, error) {
	var users []User
	err := r.db.WithContext(ctx).Where("role = ?", role).Find(&users).Error
	return users, err
}

func (r *gormUserRepo) GetUserRole(ctx context.Context, id uint) (string, error) {
	var role string
	err := r.db.WithContext(ctx).Table("users").Select("role").Where("id = ?", id).Scan(&role).Error
	return role, err
}

// --- Методы для админки ---
func (r *gormUserRepo) Count(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&User{}).Count(&count).Error
	return count, err
}

func (r *gormUserRepo) CountByRole(ctx context.Context, role string) (int64, error) {
	var count int64
	query := r.db.WithContext(ctx).Model(&User{})
	if role != "" {
		query = query.Where("role = ?", role)
	}
	err := query.Count(&count).Error
	return count, err
}

func (r *gormUserRepo) List(ctx context.Context, role string) ([]User, error) {
	var users []User
	query := r.db.WithContext(ctx).Model(&User{})
	if role != "" {
		query = query.Where("role = ?", role)
	}
	err := query.Find(&users).Error
	return users, err
}

func (r *gormUserRepo) ListPaginated(ctx context.Context, role string, offset, limit int) ([]User, error) {
	var users []User
	query := r.db.WithContext(ctx).Model(&User{})
	if role != "" {
		query = query.Where("role = ?", role)
	}
	err := query.Offset(offset).Limit(limit).Find(&users).Error
	return users, err
}

func (r *gormUserRepo) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&User{}, id).Error
}

// AtomicIncrementFailedAttempts атомарно инкрементирует failed_login_attempts
// и возвращает новое значение.
func (r *gormUserRepo) AtomicIncrementFailedAttempts(ctx context.Context, userID uint) (int, error) {
	var attempts int
	err := r.db.WithContext(ctx).
		Raw("UPDATE users SET failed_login_attempts = failed_login_attempts + 1 WHERE id = ? RETURNING failed_login_attempts", userID).
		Scan(&attempts).Error
	return attempts, err
}

type gormAchievementRepo struct{ db *gorm.DB }

func NewGormAchievementRepo(db *gorm.DB) AchievementRepository { return &gormAchievementRepo{db} }

func (r *gormAchievementRepo) Award(ctx context.Context, userID uint, achievement *Achievement) error {
	var u User
	if err := r.db.WithContext(ctx).First(&u, userID).Error; err != nil {
		return err
	}
	return r.db.WithContext(ctx).Model(&u).Association("Achievements").Append(achievement)
}
func (r *gormAchievementRepo) GetByUserID(ctx context.Context, userID uint) ([]Achievement, error) {
	var a []Achievement
	err := r.db.WithContext(ctx).Joins("JOIN user_achievements ON user_achievements.achievement_id = achievements.id").
		Where("user_achievements.user_id = ?", userID).Find(&a).Error
	return a, err
}
func (r *gormAchievementRepo) Seed(ctx context.Context) error { return nil }
func (r *gormAchievementRepo) FirstOrCreate(ctx context.Context, achievement *Achievement) error {
	return r.db.WithContext(ctx).Where("code = ?", achievement.Code).FirstOrCreate(achievement).Error
}

type gormPasswordResetRepo struct{ db *gorm.DB }

func NewGormPasswordResetRepo(db *gorm.DB) PasswordResetRepository { return &gormPasswordResetRepo{db} }
func (r *gormPasswordResetRepo) CreateToken(ctx context.Context, token *PasswordResetToken) error {
	return r.db.WithContext(ctx).Create(token).Error
}
func (r *gormPasswordResetRepo) GetToken(ctx context.Context, tokenStr string) (*PasswordResetToken, error) {
	hash := sha256.Sum256([]byte(tokenStr))
	var t PasswordResetToken
	err := r.db.WithContext(ctx).Where("token_hash = ?", hex.EncodeToString(hash[:])).First(&t).Error
	if err != nil {
		return nil, err
	}
	return &t, nil
}
func (r *gormPasswordResetRepo) GetTokenByResetCode(ctx context.Context, code string) (*PasswordResetToken, error) {
	var t PasswordResetToken
	err := r.db.WithContext(ctx).Where("reset_code = ?", code).First(&t).Error
	if err != nil {
		return nil, err
	}
	return &t, nil
}
func (r *gormPasswordResetRepo) DeleteToken(ctx context.Context, token *PasswordResetToken) error {
	return r.db.WithContext(ctx).Delete(token).Error
}
func (r *gormPasswordResetRepo) MarkTokenUsed(ctx context.Context, id uint, usedAt time.Time) error {
	return r.db.WithContext(ctx).Model(&PasswordResetToken{}).Where("id = ?", id).Update("used_at", usedAt).Error
}

type gormEmailVerificationRepo struct{ db *gorm.DB }

func NewGormEmailVerificationRepo(db *gorm.DB) EmailVerificationRepository {
	return &gormEmailVerificationRepo{db}
}
func (r *gormEmailVerificationRepo) CreateToken(ctx context.Context, token *EmailVerificationToken) error {
	return r.db.WithContext(ctx).Create(token).Error
}
func (r *gormEmailVerificationRepo) GetToken(ctx context.Context, tokenStr string) (*EmailVerificationToken, error) {
	hash := sha256.Sum256([]byte(tokenStr))
	var t EmailVerificationToken
	err := r.db.WithContext(ctx).Where("token_hash = ?", hex.EncodeToString(hash[:])).First(&t).Error
	if err != nil {
		return nil, err
	}
	return &t, nil
}
func (r *gormEmailVerificationRepo) DeleteToken(ctx context.Context, token *EmailVerificationToken) error {
	return r.db.WithContext(ctx).Delete(token).Error
}
func (r *gormEmailVerificationRepo) GetTokenByCode(ctx context.Context, code string) (*EmailVerificationToken, error) {
	var t EmailVerificationToken
	err := r.db.WithContext(ctx).Where("verification_code = ?", code).First(&t).Error
	if err != nil {
		return nil, err
	}
	return &t, nil
}
func (r *gormEmailVerificationRepo) DeleteByTokenHash(ctx context.Context, tokenHash string) error {
	return r.db.WithContext(ctx).Where("token_hash = ?", tokenHash).Delete(&EmailVerificationToken{}).Error
}
func (r *gormEmailVerificationRepo) DeleteByUserID(ctx context.Context, userID uint) error {
	return r.db.WithContext(ctx).Where("user_id = ?", userID).Delete(&EmailVerificationToken{}).Error
}

type gormExternalLoginRepo struct{ db *gorm.DB }

func NewGormExternalLoginRepo(db *gorm.DB) ExternalLoginRepository { return &gormExternalLoginRepo{db} }
func (r *gormExternalLoginRepo) FindOrCreate(ctx context.Context, login *ExternalLogin) error {
	return r.db.WithContext(ctx).Where("provider = ? AND external_id = ?", login.Provider, login.ExternalID).
		FirstOrCreate(login).Error
}

// ---------- GORM implementation for RefreshTokenRepository (добавлен) ----------

type gormRefreshTokenRepo struct{ db *gorm.DB }

func NewGormRefreshTokenRepo(db *gorm.DB) RefreshTokenRepository {
	return &gormRefreshTokenRepo{db: db}
}

func (r *gormRefreshTokenRepo) Create(ctx context.Context, token *RefreshToken) error {
	return r.db.WithContext(ctx).Create(token).Error
}

func (r *gormRefreshTokenRepo) GetByTokenHash(ctx context.Context, tokenHash string) (*RefreshToken, error) {
	var token RefreshToken
	err := r.db.WithContext(ctx).
		Where("token_hash = ? AND revoked_at IS NULL", tokenHash).
		First(&token).Error
	if err != nil {
		return nil, err
	}
	return &token, nil
}

func (r *gormRefreshTokenRepo) Revoke(ctx context.Context, id uint) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&RefreshToken{}).
		Where("id = ?", id).
		Update("revoked_at", now).Error
}

func (r *gormRefreshTokenRepo) RevokeAllForUser(ctx context.Context, userID uint) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&RefreshToken{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", now).Error
}

func (r *gormRefreshTokenRepo) DeleteExpired(ctx context.Context) error {
	return r.db.WithContext(ctx).
		Where("expires_at < ?", time.Now()).
		Delete(&RefreshToken{}).Error
}
