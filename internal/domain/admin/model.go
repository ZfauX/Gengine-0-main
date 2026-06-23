// internal/domain/admin/model.go
package admin

import (
	"time"

	"gengine-0/internal/domain/user"

	"gorm.io/gorm"
)

// AuditLog — запись в журнале аудита действий пользователей.
type AuditLog struct {
	gorm.Model
	UserID     uint      `gorm:"not null;index"`
	User       user.User `gorm:"foreignKey:UserID"`
	Action     string    `gorm:"not null"`
	ObjectType string    `gorm:"not null"`
	ObjectID   uint      `gorm:"not null"`
	Details    string    `gorm:"type:text"`
}

// Backup — информация о резервной копии базы данных.
type Backup struct {
	ID        uint      `gorm:"primaryKey"`
	Filename  string    `gorm:"not null"`
	FilePath  string    `gorm:"not null"`
	Size      int64
	CreatedAt time.Time
}
