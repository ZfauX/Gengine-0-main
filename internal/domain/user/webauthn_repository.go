package user

import (
	"context"

	"gorm.io/gorm"
)

type WebAuthnRepository interface {
	Create(ctx context.Context, credential *WebAuthnCredential) error
	GetByCredentialID(ctx context.Context, credentialID []byte) (*WebAuthnCredential, error)
	ListByUserID(ctx context.Context, userID uint) ([]WebAuthnCredential, error)
	UpdateSignCount(ctx context.Context, id uint, signCount uint32, backupState bool) error
	Delete(ctx context.Context, id uint, userID uint) error
	DeleteAllForUser(ctx context.Context, userID uint) error
}

type gormWebAuthnRepo struct {
	db *gorm.DB
}

func NewGormWebAuthnRepo(db *gorm.DB) WebAuthnRepository {
	return &gormWebAuthnRepo{db: db}
}

func (r *gormWebAuthnRepo) Create(ctx context.Context, credential *WebAuthnCredential) error {
	return r.db.WithContext(ctx).Create(credential).Error
}

func (r *gormWebAuthnRepo) GetByCredentialID(ctx context.Context, credentialID []byte) (*WebAuthnCredential, error) {
	var c WebAuthnCredential
	err := r.db.WithContext(ctx).Where("credential_id = ?", credentialID).First(&c).Error
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *gormWebAuthnRepo) ListByUserID(ctx context.Context, userID uint) ([]WebAuthnCredential, error) {
	var creds []WebAuthnCredential
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).Find(&creds).Error
	return creds, err
}

func (r *gormWebAuthnRepo) UpdateSignCount(ctx context.Context, id uint, signCount uint32, backupState bool) error {
	return r.db.WithContext(ctx).Model(&WebAuthnCredential{}).Where("id = ?", id).Updates(map[string]any{
		"sign_count":   signCount,
		"backup_state": backupState,
	}).Error
}

func (r *gormWebAuthnRepo) Delete(ctx context.Context, id uint, userID uint) error {
	return r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).Delete(&WebAuthnCredential{}).Error
}

func (r *gormWebAuthnRepo) DeleteAllForUser(ctx context.Context, userID uint) error {
	return r.db.WithContext(ctx).Where("user_id = ?", userID).Delete(&WebAuthnCredential{}).Error
}
