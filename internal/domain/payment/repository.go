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
	// GetPendingByUserAndAmount (DEEP-REVIEW PASS-5 H1): незакрытый pending-платёж
	// пользователя на ту же сумму — переиспользуется при ретрае вместо создания
	// дубликата (раньше идемпотентность не работала: ключ генерировался заново).
	GetPendingByUserAndAmount(ctx context.Context, userID uint, amountKopecks int64) (*Payment, error)
	ListByUser(ctx context.Context, userID uint, limit int) ([]Payment, error)
	UpdateStatus(ctx context.Context, id uint, status string) error
	// MarkSucceededIfPending (L6, PASS-7): атомарный переход pending→succeeded
	// (UPDATE WHERE status <> 'succeeded'); true, если переход совершён этим вызовом.
	MarkSucceededIfPending(ctx context.Context, id uint) (bool, error)
	// CancelIfPending (PASS-8 #3): атомарный переход pending→canceled
	// (UPDATE WHERE status = 'pending') — не откатывает succeeded.
	CancelIfPending(ctx context.Context, id uint) error
	// UpdateAfterCreate обновляет платёж после успешного ответа ЮKassa
	// (реальный payment_id, статус, URL подтверждения). DEEP-REVIEW HIGH #7.
	UpdateAfterCreate(ctx context.Context, id uint, paymentID, status, confirmationURL string) error
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

func (r *gormPaymentRepo) GetPendingByUserAndAmount(ctx context.Context, userID uint, amountKopecks int64) (*Payment, error) {
	var p Payment
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND amount_kopecks = ? AND status = ?", userID, amountKopecks, StatusPending).
		Order("created_at DESC").
		First(&p).Error
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

// MarkSucceededIfPending (L6, PASS-7): атомарный переход в succeeded —
// только если платёж ещё не succeeded. RowsAffected>0 означает, что именно
// ЭТОТ вызов совершил переход (устраняет гонку двух параллельных webhook'ов,
// где оба читали pending и слали дубликат уведомления).
func (r *gormPaymentRepo) MarkSucceededIfPending(ctx context.Context, id uint) (bool, error) {
	res := r.db.WithContext(ctx).Model(&Payment{}).
		Where("id = ? AND status <> ?", id, StatusSucceeded).
		Update("status", StatusSucceeded)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// CancelIfPending (PASS-8 #3): атомарный переход pending→canceled —
// не трогает succeeded (отмена/спор после оплаты не сбрасывает подтверждение).
func (r *gormPaymentRepo) CancelIfPending(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Model(&Payment{}).
		Where("id = ? AND status = ?", id, StatusPending).
		Update("status", StatusCanceled).Error
}

func (r *gormPaymentRepo) UpdateAfterCreate(ctx context.Context, id uint, paymentID, status, confirmationURL string) error {
	return r.db.WithContext(ctx).Model(&Payment{}).Where("id = ?", id).Updates(map[string]any{
		"payment_id":       paymentID,
		"status":           status,
		"confirmation_url": confirmationURL,
	}).Error
}
