// internal/domain/notification/repository.go
package notification

import (
	"context"
	"time"

	"gengine-0/internal/domain/user"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// NotificationRepository определяет контракт для работы с уведомлениями
// и настройками (D1: изолирует SQL от NotificationService — бизнес-логика
// тестируема с моком без PostgreSQL).
type NotificationRepository interface {
	// Settings
	GetByUserID(ctx context.Context, userID uint) (*user.NotificationSetting, error)
	// UpsertSettings атомарно создаёт/обновляет настройки пользователя (ON CONFLICT).
	UpsertSettings(ctx context.Context, userID uint, settingsJSON string) error

	// Notifications
	CreateNotification(ctx context.Context, n *Notification) error
	CountUnread(ctx context.Context, userID uint) (int64, error)
	ListByUser(ctx context.Context, userID uint, offset, limit int, onlyUnread bool) ([]Notification, int64, error)
	// ListRecentByUser (M7, PASS-18): лёгкая выборка последних N уведомлений
	// без COUNT(*) OVER() и полных колонок — для дашборда.
	ListRecentByUser(ctx context.Context, userID uint, limit int) ([]Notification, error)
	MarkAsRead(ctx context.Context, userID, notificationID uint) (bool, error)
	MarkAllAsRead(ctx context.Context, userID uint) error
	// DeleteOldRead удаляет прочитанные уведомления старше cutoff (P-2, pass 33:
	// retention — таблица иначе растёт безгранично).
	DeleteOldRead(ctx context.Context, cutoff time.Time) (int64, error)

	// Push subscriptions
	ListPushSubscriptions(ctx context.Context, userID uint) ([]user.PushSubscription, error)
	DeletePushSubscription(ctx context.Context, id uint) error

	// Passing lookup (для SSE broadcast времени)
	GetGamePassingGameID(ctx context.Context, passingID uint) (uint, error)
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

func (r *gormNotificationRepo) UpsertSettings(ctx context.Context, userID uint, settingsJSON string) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"settings_json": settingsJSON,
			"updated_at":    time.Now(),
		}),
	}).Create(&user.NotificationSetting{
		UserID:       userID,
		SettingsJSON: settingsJSON,
	}).Error
}

func (r *gormNotificationRepo) CreateNotification(ctx context.Context, n *Notification) error {
	// M6 (PASS-16): DoNothing на конфликте — частичный уникальный индекс
	// (user_id, game_id) WHERE type='game_reminder' защищает от дубликатов
	// напоминаний о предстоящих играх (см. миграцию 000068). Других уникальных
	// индексов на notifications нет, поэтому DoNothing затрагивает только
	// game_reminder.
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(n).Error
}

func (r *gormNotificationRepo) CountUnread(ctx context.Context, userID uint) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&Notification{}).
		Where("user_id = ? AND read = ?", userID, false).Count(&count).Error
	return count, err
}

func (r *gormNotificationRepo) ListByUser(ctx context.Context, userID uint, offset, limit int, onlyUnread bool) ([]Notification, int64, error) {
	// F-7 (pass 31): COUNT(*) OVER() — total в том же запросе, без отдельного COUNT.
	// F-4 (pass 48): фильтр «только непрочитанные».
	type notificationRow struct {
		Notification
		TotalCount int64
	}
	var rows []notificationRow
	q := r.db.WithContext(ctx).
		Select("notifications.*, COUNT(*) OVER() AS total_count").
		Model(&Notification{}).
		Where("user_id = ?", userID)
	if onlyUnread {
		q = q.Where("read = ?", false)
	}
	err := q.Order("created_at DESC").
		Offset(offset).Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	total := int64(0)
	notifications := make([]Notification, 0, len(rows))
	for i := range rows {
		if i == 0 {
			total = rows[i].TotalCount
		}
		notifications = append(notifications, rows[i].Notification)
	}
	return notifications, total, nil
}

// ListRecentByUser — лёгкая выборка последних N уведомлений (M7, PASS-18):
// без window-count и лишних колонок; для дашборда «последние уведомления».
func (r *gormNotificationRepo) ListRecentByUser(ctx context.Context, userID uint, limit int) ([]Notification, error) {
	var notifications []Notification
	err := r.db.WithContext(ctx).
		Select("id", "type", "title", "body", "link", "read", "created_at").
		Where("user_id = ?", userID).
		Order("created_at DESC").
		Limit(limit).
		Find(&notifications).Error
	return notifications, err
}

func (r *gormNotificationRepo) MarkAsRead(ctx context.Context, userID, notificationID uint) (bool, error) {
	res := r.db.WithContext(ctx).Model(&Notification{}).
		Where("id = ? AND user_id = ?", notificationID, userID).
		Updates(map[string]any{"read": true, "read_at": time.Now()})
	return res.RowsAffected > 0, res.Error
}

func (r *gormNotificationRepo) MarkAllAsRead(ctx context.Context, userID uint) error {
	// M2 (PASS-20): ставим read_at, чтобы DeleteOldRead мог чистить пакетно
	// прочитанные уведомления. Раньше read_at оставался NULL → условие
	// read_at < cutoff не срабатывало → уведомления накапливались бессрочно.
	return r.db.WithContext(ctx).Model(&Notification{}).
		Where("user_id = ? AND read = ?", userID, false).
		Updates(map[string]any{"read": true, "read_at": time.Now()}).Error
}

// DeleteOldRead удаляет прочитанные уведомления старше cutoff (P-2, pass 33).
func (r *gormNotificationRepo) DeleteOldRead(ctx context.Context, cutoff time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Where("read = ? AND read_at < ?", true, cutoff).Delete(&Notification{})
	return res.RowsAffected, res.Error
}

func (r *gormNotificationRepo) ListPushSubscriptions(ctx context.Context, userID uint) ([]user.PushSubscription, error) {
	var subs []user.PushSubscription
	// P-M4 (PASS-8): защитный LIMIT — подписок на push у пользователя немного.
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).Limit(100).Find(&subs).Error
	return subs, err
}

func (r *gormNotificationRepo) DeletePushSubscription(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&user.PushSubscription{}).Error
}

func (r *gormNotificationRepo) GetGamePassingGameID(ctx context.Context, passingID uint) (uint, error) {
	var passing struct {
		GameID uint
	}
	err := r.db.WithContext(ctx).Table("game_passings").
		Select("game_id").Where("id = ?", passingID).Scan(&passing).Error
	return passing.GameID, err
}
