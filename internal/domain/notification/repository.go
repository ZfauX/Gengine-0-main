// internal/domain/notification/repository.go
package notification

import (
	"context"

	"gengine-0/internal/domain/user"

	"gorm.io/gorm"
)

// NotificationRepository определяет контракт для работы с настройками уведомлений.
type NotificationRepository interface {
	GetByUserID(ctx context.Context, userID uint) (*user.NotificationSetting, error)
	Save(ctx context.Context, settings *user.NotificationSetting) error
}

type gormNotificationRepo struct {
	db *gorm.DB
}

func NewNotificationRepository(db *gorm.DB) NotificationRepository {
	return &gormNotificationRepo{db: db}
}

func (r *gormNotificationRepo) GetByUserID(ctx context.Context, userID uint) (*user.NotificationSetting, error) {
	var settings user.NotificationSetting
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&settings).Error
	if err != nil {
		return nil, err
	}
	return &settings, nil
}

func (r *gormNotificationRepo) Save(ctx context.Context, settings *user.NotificationSetting) error {
	return r.db.WithContext(ctx).Save(settings).Error
}
