// internal/domain/notification/service.go
package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gengine-0/internal/domain/user"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// NotificationType определяет тип уведомления
type NotificationType string

const (
	NotificationTypeApplicationAccepted NotificationType = "application_accepted"
	NotificationTypeApplicationRejected NotificationType = "application_rejected"
	NotificationTypeGameStarted         NotificationType = "game_started"
	NotificationTypeLevelCompleted      NotificationType = "level_completed"
	NotificationTypeNewMessage          NotificationType = "new_message"
	NotificationTypeTimeWarning         NotificationType = "time_warning"
	NotificationTypeTimeExpired         NotificationType = "time_expired"
)

// Notification представляет уведомление в БД
type Notification struct {
	gorm.Model
	UserID    uint             `json:"user_id" gorm:"index"`
	Type      NotificationType `json:"type" gorm:"size:50"`
	Title     string           `json:"title"`
	Message   string           `json:"message"`
	URL       string           `json:"url,omitempty"`
	Read      bool             `json:"read" gorm:"default:false;index"`
	Data      string           `json:"data,omitempty" gorm:"type:text"`
	CreatedAt time.Time        `json:"created_at"`
}

// TableName переопределяет имя таблицы
func (Notification) TableName() string {
	return "notifications"
}

// NotificationService отвечает за работу с настройками и push-уведомлениями.
type NotificationService struct {
	repo NotificationRepository
	db   *gorm.DB
	hub  *ws.RoomHub
}

func NewNotificationService(db *gorm.DB, hub *ws.RoomHub) *NotificationService {
	return &NotificationService{
		repo: NewNotificationRepository(db),
		db:   db,
		hub:  hub,
	}
}

// WithHub устанавливает WebSocket-хаб для push-уведомлений
func (s *NotificationService) WithHub(hub *ws.RoomHub) *NotificationService {
	s.hub = hub
	return s
}

// Settings структура настроек уведомлений.
// Поддерживает гранулярные настройки по типам событий и каналам.
type Settings struct {
	EmailEnabled   bool `json:"email_enabled"`   // Включить email-уведомления
	PushEnabled    bool `json:"push_enabled"`    // Включить push-уведомления
	BrowserEnabled bool `json:"browser_enabled"` // Включить браузерные уведомления

	// Granular settings: какие события отправлять по email
	EmailGameStarted         bool `json:"email_game_started"`
	EmailLevelCompleted      bool `json:"email_level_completed"`
	EmailApplicationAccepted bool `json:"email_application_accepted"`
	EmailApplicationRejected bool `json:"email_application_rejected"`
	EmailTimeWarning         bool `json:"email_time_warning"`
	EmailTimeExpired         bool `json:"email_time_expired"`
}

// DefaultSettings возвращает настройки по умолчанию
func DefaultSettings() *Settings {
	return &Settings{
		EmailEnabled:             true,
		PushEnabled:              false,
		BrowserEnabled:           true,
		EmailGameStarted:         true,
		EmailLevelCompleted:      true,
		EmailApplicationAccepted: true,
		EmailApplicationRejected: false,
		EmailTimeWarning:         true,
		EmailTimeExpired:         true,
	}
}

// GetSettings возвращает настройки пользователя.
func (s *NotificationService) GetSettings(ctx context.Context, userID uint) (*Settings, error) {
	settings, err := s.repo.GetByUserID(ctx, userID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			// Возвращаем настройки по умолчанию
			return DefaultSettings(), nil
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

// GetEmailNotificationFlags возвращает только флаги email-уведомлений для фронтенда
func (s *NotificationService) GetEmailNotificationFlags(ctx context.Context, userID uint) (map[string]any, error) {
	settings, err := s.GetSettings(ctx, userID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"email_enabled":              settings.EmailEnabled,
		"email_game_started":         settings.EmailGameStarted,
		"email_level_completed":      settings.EmailLevelCompleted,
		"email_application_accepted": settings.EmailApplicationAccepted,
		"email_application_rejected": settings.EmailApplicationRejected,
		"email_time_warning":         settings.EmailTimeWarning,
		"email_time_expired":         settings.EmailTimeExpired,
		"browser_enabled":            settings.BrowserEnabled,
		"push_enabled":               settings.PushEnabled,
	}, nil
}

// Create создаёт новое push-уведомление
func (s *NotificationService) Create(ctx context.Context, userID uint, ntype NotificationType, title, message, url, data string) error {
	notification := &Notification{
		UserID:  userID,
		Type:    ntype,
		Title:   title,
		Message: message,
		URL:     url,
		Data:    data,
		Read:    false,
	}

	if err := s.db.WithContext(ctx).Create(notification).Error; err != nil {
		return fmt.Errorf("failed to create notification: %w", err)
	}

	// Отправляем WebSocket-уведомление в реальном времени
	if s.hub != nil {
		s.sendWebSocketNotification(userID, notification)
	}

	log.Debug().Uint("user_id", userID).Str("type", string(ntype)).Msg("Notification created")
	return nil
}

// sendWebSocketNotification отправляет уведомление через WebSocket
func (s *NotificationService) sendWebSocketNotification(userID uint, notification *Notification) {
	roomID := fmt.Sprintf("user:%d", userID)

	notificationData := map[string]any{
		"type":         string(notification.Type),
		"id":           notification.ID,
		"title":        notification.Title,
		"message":      notification.Message,
		"url":          notification.URL,
		"created_at":   notification.CreatedAt.Format(time.RFC3339),
		"unread_count": s.getUnreadCount(userID),
	}

	data, err := json.Marshal(notificationData)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("Failed to marshal notification")
		return
	}

	s.hub.BroadcastToRoom(roomID, data)
}

// getUnreadCount возвращает количество непрочитанных уведомлений
func (s *NotificationService) getUnreadCount(userID uint) int {
	var count int64
	s.db.Model(&Notification{}).Where("user_id = ? AND read = ?", userID, false).Count(&count)
	return int(count)
}

// GetByUser возвращает уведомления пользователя с пагинацией
func (s *NotificationService) GetByUser(ctx context.Context, userID uint, page, perPage int) ([]Notification, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 20
	}

	var total int64
	var notifications []Notification

	query := s.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at DESC")
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * perPage
	if err := query.Offset(offset).Limit(perPage).Find(&notifications).Error; err != nil {
		return nil, 0, err
	}

	return notifications, total, nil
}

// MarkAsRead помечает уведомление как прочитанное
func (s *NotificationService) MarkAsRead(ctx context.Context, userID, notificationID uint) error {
	result := s.db.WithContext(ctx).Model(&Notification{}).
		Where("id = ? AND user_id = ?", notificationID, userID).
		Update("read", true)

	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("notification not found")
	}

	return nil
}

// MarkAllAsRead помечает все уведомления пользователя как прочитанные
func (s *NotificationService) MarkAllAsRead(ctx context.Context, userID uint) error {
	return s.db.WithContext(ctx).Model(&Notification{}).
		Where("user_id = ? AND read = ?", userID, false).
		Update("read", true).Error
}

// GetUnreadCount возвращает количество непрочитанных уведомлений
func (s *NotificationService) GetUnreadCount(userID uint) int {
	return s.getUnreadCount(userID)
}

// SendTimeWarning отправляет предупреждение о таймере
func (s *NotificationService) SendTimeWarning(ctx context.Context, userID uint, passingID uint, remainingSeconds int) error {
	title := "Внимание! Ограничение по времени"
	message := fmt.Sprintf("До завершения уровня осталось %d секунд", remainingSeconds)
	url := fmt.Sprintf("/game/%d", passingID)

	return s.Create(ctx, userID, NotificationTypeTimeWarning, title, message, url, fmt.Sprintf(`{"passing_id":%d,"remaining":%d}`, passingID, remainingSeconds))
}

// SendTimeExpired отправляет уведомление об истечении времени
func (s *NotificationService) SendTimeExpired(ctx context.Context, userID uint, passingID uint) error {
	title := "Время вышло!"
	message := "Время на прохождение уровня истекло. Уровень автоматически завершён."
	url := fmt.Sprintf("/game/%d", passingID)

	return s.Create(ctx, userID, NotificationTypeTimeExpired, title, message, url, fmt.Sprintf(`{"passing_id":%d}`, passingID))
}
