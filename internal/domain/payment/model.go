// internal/domain/payment/model.go
// G-1..G-3 (pass 45): платёжная модель ЮKassa.
package payment

import (
	"time"
)

// Статусы платежа ЮKassa.
const (
	StatusPending   = "pending"   // создан, ждём подтверждения
	StatusSucceeded = "succeeded" // оплачен
	StatusCanceled  = "canceled"  // отменён/просрочен
)

// Payment — запись о платеже.
type Payment struct {
	ID        uint `gorm:"primaryKey"`
	CreatedAt time.Time
	UpdatedAt time.Time

	UserID uint `gorm:"not null;index:idx_payments_user"`
	// PaymentID — ID платежа в ЮKassa (внешний).
	PaymentID string `gorm:"uniqueIndex;not null"`
	// IdempotencyKey — ключ идемпотентности (повторное создание не дублирует).
	IdempotencyKey string `gorm:"index"`
	// AmountKopecks (DEEP-REVIEW PASS-3 M10): сумма в КОПЕЙКАХ (int64).
	// Денежная арифметика float64 заменена на целочисленную — нет погрешностей
	// округления, verifyRemoteAmount сравнивает точно.
	AmountKopecks int64
	Currency      string `gorm:"default:'RUB'"`
	Description   string
	Status        string `gorm:"default:'pending';index:idx_payments_status"`
	// ConfirmationURL — ссылка на оплату, выдаваемая ЮKassa.
	ConfirmationURL string
	// Metadata — произвольные данные (например, type=game, game_id=...).
	Metadata string `gorm:"type:text"`
}

// AmountRubles возвращает сумму в рублях для отображения/логов (без арифметики).
func (p *Payment) AmountRubles() float64 {
	return float64(p.AmountKopecks) / 100.0
}

func (Payment) TableName() string { return "payments" }
