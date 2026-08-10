// internal/domain/payment/repository.go
package payment

import (
	"context"

	"gorm.io/gorm"
)

// PaymentRepository — контракт хранения платежей.
type PaymentRepository interface {
	Create(ctx context.Context, p *Payment) error
	GetByID(ctx context.Context, id uint) (*Payment, error)
	GetByPaymentID(ctx context.Context, paymentID string) (*Payment, error)
	GetByIdempotencyKey(ctx context.Context, key string) (*Payment, error)
	ListByUser(ctx context.Context, userID uint, limit int) ([]Payment, error)
	UpdateStatus(ctx context.Context, id uint, status string) error
}

type gormPaymentRepo struct{ db *gorm.DB }

func NewGormPaymentRepo(db *gorm.DB) PaymentRepository {
	return &gormPaymentRepo{db: db}
}

func (r *gormPaymentRepo) Create(ctx context.Context, p *Payment) error {
	return r.db.WithContext(ctx).Create(p).Error
}

func (r *gormPaymentRepo) GetByID(ctx context.Context, id uint) (*Payment, error) {
	var p Payment
	err := r.db.WithContext(ctx).First(&p, id).Error
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *gormPaymentRepo) GetByPaymentID(ctx context.Context, paymentID string) (*Payment, error) {
	var p Payment
	err := r.db.WithContext(ctx).Where("payment_id = ?", paymentID).First(&p).Error
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *gormPaymentRepo) GetByIdempotencyKey(ctx context.Context, key string) (*Payment, error) {
	var p Payment
	err := r.db.WithContext(ctx).Where("idempotency_key = ?", key).First(&p).Error
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *gormPaymentRepo) ListByUser(ctx context.Context, userID uint, limit int) ([]Payment, error) {
	var ps []Payment
	if limit < 1 || limit > 100 {
		limit = 50
	}
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at DESC").Limit(limit).Find(&ps).Error
	return ps, err
}

func (r *gormPaymentRepo) UpdateStatus(ctx context.Context, id uint, status string) error {
	return r.db.WithContext(ctx).Model(&Payment{}).Where("id = ?", id).Update("status", status).Error
}
