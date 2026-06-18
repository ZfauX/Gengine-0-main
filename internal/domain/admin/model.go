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
	Action     string    `gorm:"not null"`          // например, "game_created", "team_disqualified"
	ObjectType string    `gorm:"not null"`          // "game", "level", "team", "passing" и т.д.
	ObjectID   uint      `gorm:"not null"`
	Details    string    `gorm:"type:text"`         // JSON или произвольное описание изменений
}

// Backup — информация о резервной копии базы данных.
type Backup struct {
	ID        uint      `gorm:"primaryKey"`
	Filename  string    `gorm:"not null"`
	FilePath  string    `gorm:"not null"`
	Size      int64
	CreatedAt time.Time
}
