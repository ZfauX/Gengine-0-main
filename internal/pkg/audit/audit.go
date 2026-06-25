// internal/pkg/audit/audit.go
package audit

import (
	"context"
	"strconv"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// Entry — запись в журнале аудита действий пользователей.
type Entry struct {
	gorm.Model
	UserID     uint   `gorm:"not null;index"`
	Action     string `gorm:"not null"`
	ObjectType string `gorm:"not null"`
	ObjectID   uint   `gorm:"not null"`
	Details    string `gorm:"type:text"`
}

func (Entry) TableName() string { return "audit_logs" }

// EntryWithUser — запись аудита с именем пользователя (join).
type EntryWithUser struct {
	ID         uint   `json:"id"`
	CreatedAt  string `json:"created_at"`
	UserID     uint   `json:"user_id"`
	UserName   string `json:"user_name"`
	Action     string `json:"action"`
	ObjectType string `json:"object_type"`
	ObjectID   uint   `json:"object_id"`
	Details    string `json:"details"`
}

// Service записывает и читает события аудита.
type Service struct {
	DB *gorm.DB
}

// NewService создаёт новый Service.
func NewService(db *gorm.DB) *Service {
	return &Service{DB: db}
}

// Log создаёт запись аудита. Ошибки только логирует (не прерывает бизнес-логику).
func (s *Service) Log(userID uint, action, objectType string, objectID uint, details string) {
	e := Entry{
		UserID:     userID,
		Action:     action,
		ObjectType: objectType,
		ObjectID:   objectID,
		Details:    details,
	}
	if err := s.DB.Create(&e).Error; err != nil {
		log.Error().Err(err).
			Str("action", action).
			Uint("user", userID).
			Msg("audit: failed to log entry")
	}
}

// Count возвращает общее количество записей аудита.
func (s *Service) Count(ctx context.Context) (int64, error) {
	var count int64
	err := s.DB.WithContext(ctx).Model(&Entry{}).Count(&count).Error
	return count, err
}

// List возвращает записи аудита с пагинацией и фильтрацией.
// Добавлен контекст.
func (s *Service) List(ctx context.Context, userIDStr, action string, page, perPage int) ([]EntryWithUser, int64, error) {
	base := s.DB.WithContext(ctx).Table("audit_logs").
		Joins("LEFT JOIN users ON users.id = audit_logs.user_id")

	countQ := s.DB.WithContext(ctx).Table("audit_logs")
	if userIDStr != "" {
		if id, err := strconv.Atoi(userIDStr); err == nil {
			base = base.Where("audit_logs.user_id = ?", id)
			countQ = countQ.Where("user_id = ?", id)
		}
	}
	if action != "" {
		base = base.Where("audit_logs.action = ?", action)
		countQ = countQ.Where("action = ?", action)
	}

	var total int64
	countQ.Count(&total)

	var rows []EntryWithUser
	offset := (page - 1) * perPage
	err := base.
		Select("audit_logs.id, audit_logs.created_at, audit_logs.user_id, users.name AS user_name, audit_logs.action, audit_logs.object_type, audit_logs.object_id, audit_logs.details").
		Order("audit_logs.created_at DESC").
		Offset(offset).
		Limit(perPage).
		Scan(&rows).Error

	return rows, total, err
}
