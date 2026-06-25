// Package user — repository interfaces for user domain.
package user

import (
	"context"

	"gorm.io/gorm"
)

// UserRepository определяет контракт для работы с пользователями.
type UserRepository interface {
	Create(ctx context.Context, user *User) error
	GetByID(ctx context.Context, id uint) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	GetPublicProfile(ctx context.Context, id uint) (*User, error)
	Update(ctx context.Context, id uint, fields map[string]any) error
	GetByRole(ctx context.Context, role string) (*User, error)
	// Добавленные методы для админки
	Count(ctx context.Context) (int64, error)
	List(ctx context.Context, role string) ([]User, error)
	Delete(ctx context.Context, id uint) error
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
	DeleteToken(ctx context.Context, token *PasswordResetToken) error
}

// EmailVerificationRepository — контракт для верификации email.
type EmailVerificationRepository interface {
	CreateToken(ctx context.Context, token *EmailVerificationToken) error
	GetToken(ctx context.Context, tokenStr string) (*EmailVerificationToken, error)
	DeleteToken(ctx context.Context, token *EmailVerificationToken) error
}

// ExternalLoginRepository — контракт для OAuth-привязок.
type ExternalLoginRepository interface {
	FindOrCreate(ctx context.Context, login *ExternalLogin) error
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
	return &u, err
}
func (r *gormUserRepo) GetByEmail(ctx context.Context, email string) (*User, error) {
	var u User
	err := r.db.WithContext(ctx).Where("email = ?", email).First(&u).Error
	return &u, err
}
func (r *gormUserRepo) GetPublicProfile(ctx context.Context, id uint) (*User, error) {
	var u User
	err := r.db.WithContext(ctx).Preload("Achievements").First(&u, id).Error
	return &u, err
}
func (r *gormUserRepo) Update(ctx context.Context, id uint, fields map[string]any) error {
	return r.db.WithContext(ctx).Model(&User{}).Where("id = ?", id).Updates(fields).Error
}
func (r *gormUserRepo) GetByRole(ctx context.Context, role string) (*User, error) {
	var u User
	err := r.db.WithContext(ctx).Where("role = ?", role).First(&u).Error
	return &u, err
}

// --- Новые методы ---
func (r *gormUserRepo) Count(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&User{}).Count(&count).Error
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

func (r *gormUserRepo) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&User{}, id).Error
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
	var t PasswordResetToken
	err := r.db.WithContext(ctx).Where("token = ?", tokenStr).First(&t).Error
	return &t, err
}
func (r *gormPasswordResetRepo) DeleteToken(ctx context.Context, token *PasswordResetToken) error {
	return r.db.WithContext(ctx).Delete(token).Error
}

type gormEmailVerificationRepo struct{ db *gorm.DB }

func NewGormEmailVerificationRepo(db *gorm.DB) EmailVerificationRepository {
	return &gormEmailVerificationRepo{db}
}
func (r *gormEmailVerificationRepo) CreateToken(ctx context.Context, token *EmailVerificationToken) error {
	return r.db.WithContext(ctx).Create(token).Error
}
func (r *gormEmailVerificationRepo) GetToken(ctx context.Context, tokenStr string) (*EmailVerificationToken, error) {
	var t EmailVerificationToken
	err := r.db.WithContext(ctx).Where("token = ?", tokenStr).First(&t).Error
	return &t, err
}
func (r *gormEmailVerificationRepo) DeleteToken(ctx context.Context, token *EmailVerificationToken) error {
	return r.db.WithContext(ctx).Delete(token).Error
}

type gormExternalLoginRepo struct{ db *gorm.DB }

func NewGormExternalLoginRepo(db *gorm.DB) ExternalLoginRepository { return &gormExternalLoginRepo{db} }
func (r *gormExternalLoginRepo) FindOrCreate(ctx context.Context, login *ExternalLogin) error {
	return r.db.WithContext(ctx).Where("provider = ? AND external_id = ?", login.Provider, login.ExternalID).
		FirstOrCreate(login).Error
}
