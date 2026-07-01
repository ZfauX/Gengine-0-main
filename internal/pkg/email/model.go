// internal/pkg/email/model.go
package email

import (
	"time"

	"gorm.io/gorm"
)

// QueuedEmail хранит письма, ожидающие отправки в базе данных.
type QueuedEmail struct {
	ID        uint           `gorm:"primaryKey"`
	CreatedAt time.Time      `gorm:"index"`
	UpdatedAt time.Time      `gorm:"index"`
	DeletedAt gorm.DeletedAt `gorm:"index"`

	Recipient   string     `gorm:"not null"` // получатель (email)
	Subject     string     `gorm:"not null"`
	Body        string     `gorm:"type:text;not null"`
	Status      string     `gorm:"default:'pending';index"` // pending, sent, failed
	Attempts    int        `gorm:"default:0"`
	LastError   string     `gorm:"type:text"`
	ScheduledAt *time.Time `gorm:"index"`
	SentAt      *time.Time
}
