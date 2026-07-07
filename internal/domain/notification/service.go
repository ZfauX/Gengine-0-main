// internal/domain/notification/service.go
package notification

import (
	"context"
	"encoding/json"

	"gengine-0/internal/domain/user"

	"gorm.io/gorm"
)

// NotificationService отвечает за работу с настройками уведомлений.
type NotificationService struct {
	repo NotificationRepository
}

func NewNotificationService(db *gorm.DB) *NotificationService {
	return &NotificationService{
		repo: NewNotificationRepository(db),
	}
}

// Settings структура настроек, которая хранится в JSON.
type Settings struct {
	EmailEnabled bool `json:"email_enabled"`
	PushEnabled  bool `json:"push_enabled"`
	// Можно добавить другие поля
}

// GetSettings возвращает настройки пользователя.
func (s *NotificationService) GetSettings(ctx context.Context, userID uint) (*Settings, error) {
	settings, err := s.repo.GetByUserID(ctx, userID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			// Возвращаем настройки по умолчанию
			return &Settings{
				EmailEnabled: true,
				PushEnabled:  false,
			}, nil
		}
		return nil, err
	}
	var result Settings
	if err := json.Unmarshal([]byte(settings.SettingsJSON), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SaveSettings сохраняет настройки пользователя.
func (s *NotificationService) SaveSettings(ctx context.Context, userID uint, settings *Settings) error {
	jsonData, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	// Ищем существующую запись
	existing, err := s.repo.GetByUserID(ctx, userID)
	if err != nil && err != gorm.ErrRecordNotFound {
		return err
	}
	if err == gorm.ErrRecordNotFound {
		// Создаём новую
		newSettings := &user.NotificationSetting{
			UserID:       userID,
			SettingsJSON: string(jsonData),
		}
		return s.repo.Save(ctx, newSettings)
	}
	// Обновляем существующую
	existing.SettingsJSON = string(jsonData)
	return s.repo.Save(ctx, existing)
}
