// internal/domain/notification/service.go
package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/i18n"
	ws "gengine-0/internal/pkg/websocket"

	webpush "github.com/SherClockHolmes/webpush-go"
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

// Notification represents a user notification stored in the database.
// FK-каскады (ON DELETE CASCADE для user_id/game_id/team_id) обеспечены
// миграцией 000029_notifications_cascade — жёсткое удаление сущности
// не оставляет осиротевшие уведомления.
type Notification struct {
	ID        uint             `gorm:"primaryKey"`
	CreatedAt time.Time        `gorm:"autoCreateTime"`
	UpdatedAt time.Time        `gorm:"autoUpdateTime"`
	DeletedAt gorm.DeletedAt   `gorm:"index"`
	UserID    uint             `json:"user_id" gorm:"not null;index"`
	Type      NotificationType `json:"type" gorm:"size:50"`
	Title     string           `json:"title" gorm:"not null"`
	// Body is the notification message content in plain text.
	Body string `json:"body" gorm:"type:text;not null;default:''"`
	// Link is an optional URL the notification points to.
	Link string `json:"link,omitempty" gorm:"not null;default:''"`
	Read bool   `json:"read" gorm:"default:false;index"`
	// ReadAt is set when the notification is first read by the user.
	ReadAt *time.Time `json:"read_at,omitempty"`
	GameID *uint      `json:"game_id,omitempty"`
	TeamID *uint      `json:"team_id,omitempty"`
}

// TableName переопределяет имя таблицы
func (Notification) TableName() string {
	return "notifications"
}

// NotificationService отвечает за работу с настройками и push-уведомлениями.
// D1: не зависит от *gorm.DB напрямую — все запросы через NotificationRepository.
type NotificationService struct {
	hub      *ws.RoomHub
	repo     NotificationRepository
	sseMgr   *game.SSEManager
	vapidCfg config.VAPIDConfig
	baseURL  string

	// unreadCache — короткий TTL-кэш счётчика непрочитанных (P-M6):
	// не делать COUNT на каждое созданное уведомление.
	unreadMu    sync.Mutex
	unreadCache map[uint]unreadEntry

	// push pool (H7, pass 30): отправка Web Push идёт через фиксированный пул
	// воркеров вместо неограниченных goroutine — всплеск уведомлений больше
	// не порождает тысячи параллельных HTTP-запросов к push-провайдеру.
	pushMu       sync.Mutex
	pushJobs     chan pushJob
	pushWg       sync.WaitGroup
	pushShutting bool
}

type pushJob struct {
	userID uint
	notif  Notification
}

// Размер пула и очереди — эмпирически: 4 воркера × лимит подписок на юзера,
// очередь 256 буферизует всплеск (например, старт турнира с 200 участниками).
const (
	pushWorkerCount = 4
	pushQueueSize   = 256
	pushJobTimeout  = 60 * time.Second
)

type unreadEntry struct {
	count   int
	expires time.Time
}

const unreadCacheTTL = 30 * time.Second

func NewNotificationService(repo NotificationRepository, hub *ws.RoomHub) *NotificationService {
	return &NotificationService{
		repo:        repo,
		hub:         hub,
		unreadCache: make(map[uint]unreadEntry),
	}
}

// WithHub устанавливает WebSocket-хаб для push-уведомлений
func (s *NotificationService) WithHub(hub *ws.RoomHub) *NotificationService {
	s.hub = hub
	return s
}

// WithSSEManager устанавливает SSE-менеджер для broadcast-уведомлений.
func (s *NotificationService) WithSSEManager(sseMgr *game.SSEManager) *NotificationService {
	s.sseMgr = sseMgr
	return s
}

// WithVAPID устанавливает VAPID-ключи и базовый URL для отправки Web Push.
func (s *NotificationService) WithVAPID(cfg config.VAPIDConfig, baseURL string) *NotificationService {
	s.vapidCfg = cfg
	s.baseURL = baseURL
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
		if errors.Is(err, gorm.ErrRecordNotFound) {
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
	// Единый upsert по user_id (C-M1): закрывает гонку update-then-insert —
	// два параллельных первых сохранения больше не дают unique-violation.
	return s.repo.UpsertSettings(ctx, userID, string(jsonData))
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
func (s *NotificationService) Create(ctx context.Context, userID uint, ntype NotificationType, title, body, link string) error {
	notification := &Notification{
		UserID: userID,
		Type:   ntype,
		Title:  title,
		Body:   body,
		Link:   link,
		Read:   false,
	}

	if err := s.repo.CreateNotification(ctx, notification); err != nil {
		return fmt.Errorf("failed to create notification: %w", err)
	}

	// L-1 (pass 40): инкрементируем закэшированный unread-счётчик вместо
	// инвалидации — раньше invalidate + getUnreadCount в WS-payload давали
	// +1 COUNT из БД на каждое создание уведомления (P-M6 не достигался).
	s.incrementUnreadCount(userID)

	// Отправляем WebSocket-уведомление в реальном времени
	if s.hub != nil {
		s.sendWebSocketNotification(ctx, userID, notification)
	}

	// PF-2 (pass 29): Web Push отправляется асинхронно — синхронные HTTP-вызовы
	// push-провайдеру (по всем подпискам) блокировали request-путь. Очередь
	// воркеров (H7, pass 30): фиксированный пул вместо неограниченных
	// goroutine; контекст фоновый, не зависит от завершения запроса.
	if s.vapidCfg.PublicKey != "" && s.vapidCfg.PrivateKey != "" {
		notif := *notification
		uid := userID
		s.enqueueWebPush(uid, &notif)
	}

	log.Debug().Uint("user_id", userID).Str("type", string(ntype)).Msg("Notification created")
	return nil
}

// enqueueWebPush ставит задачу Web Push в очередь воркеров. Пул инициализируется
// лениво при первой отправке; при переполнении очереди задача отбрасывается
// (не блокируем request-путь — потеря push менее критична, чем зависание).
func (s *NotificationService) enqueueWebPush(userID uint, n *Notification) {
	job := pushJob{
		userID: userID,
		notif:  *n,
	}
	s.pushMu.Lock()
	// S1 (pass 30): после Shutdown новые задачи отбрасываем — иначе пул
	// пересоздастся и WaitGroup-счётчик инкрементится во время Wait (panic).
	if s.pushShutting {
		s.pushMu.Unlock()
		log.Warn().Uint("user_id", userID).Msg("Notification: push pool shutting down, dropping job")
		return
	}
	if s.pushJobs == nil {
		s.pushJobs = make(chan pushJob, pushQueueSize)
		for i := 0; i < pushWorkerCount; i++ {
			s.pushWg.Add(1)
			go s.pushWorker()
		}
	}
	ch := s.pushJobs
	s.pushMu.Unlock()

	select {
	case ch <- job:
	default:
		log.Warn().Uint("user_id", userID).Msg("Notification: push queue full, dropping job")
	}
}

// pushWorker обрабатывает задачи из очереди до её закрытия.
func (s *NotificationService) pushWorker() {
	defer s.pushWg.Done()
	for job := range s.pushJobs {
		ctx, cancel := context.WithTimeout(context.Background(), pushJobTimeout)
		err := s.sendWebPush(ctx, job.userID, &job.notif)
		cancel()
		if err != nil {
			log.Warn().Err(err).Uint("user_id", job.userID).Msg("Notification: web push send failed")
		}
	}
}

// Shutdown останавливает пул push-воркеров, дожидаясь доставки задач из очереди.
// Идемпотентен: повторный вызов безопасен (pushShutting уже true).
func (s *NotificationService) Shutdown() {
	s.pushMu.Lock()
	if s.pushShutting {
		s.pushMu.Unlock()
		return
	}
	s.pushShutting = true
	if s.pushJobs != nil {
		close(s.pushJobs)
		s.pushJobs = nil
	}
	s.pushMu.Unlock()
	s.pushWg.Wait()
}

// sendWebPush отправляет Web Push всем подпискам пользователя.
// Подписка является явным согласием на push — отправляем для каждого созданного уведомления.
// Устаревшие подписки (HTTP 404/410) автоматически удаляются.
func (s *NotificationService) sendWebPush(ctx context.Context, userID uint, n *Notification) error {
	if s.vapidCfg.PublicKey == "" || s.vapidCfg.PrivateKey == "" {
		return nil // VAPID не настроен — push недоступен
	}

	var subs []user.PushSubscription
	var err error
	subs, err = s.repo.ListPushSubscriptions(ctx, userID)
	if err != nil {
		return fmt.Errorf("load push subscriptions: %w", err)
	}
	if len(subs) == 0 {
		return nil
	}

	// Уважаем настройку пользователя: если push отключён в настройках — не отправляем.
	settings, err := s.GetSettings(ctx, userID)
	if err == nil && !settings.PushEnabled {
		return nil
	}
	if err != nil {
		log.Warn().Err(err).Uint("user_id", userID).Msg("sendWebPush: failed to load settings, sending anyway")
	}

	payload, err := json.Marshal(map[string]any{
		"title": n.Title,
		"body":  n.Body,
		"url":   s.baseURL + n.Link,
		"tag":   string(n.Type),
	})
	if err != nil {
		return fmt.Errorf("marshal push payload: %w", err)
	}

	opts := &webpush.Options{
		Subscriber:      s.vapidCfg.Subject,
		VAPIDPublicKey:  s.vapidCfg.PublicKey,
		VAPIDPrivateKey: s.vapidCfg.PrivateKey,
		TTL:             3600,
	}

	for _, sub := range subs {
		wsub := &webpush.Subscription{
			Endpoint: sub.Endpoint,
			Keys: webpush.Keys{
				Auth:   sub.Auth,
				P256dh: sub.P256dh,
			},
		}
		resp, err := webpush.SendNotificationWithContext(ctx, payload, wsub, opts)
		// N2: закрываем body даже при ошибке (иначе утечка соединения).
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err != nil {
			log.Warn().Err(err).Uint("user_id", userID).Uint("sub_id", sub.ID).Msg("webpush send error")
			continue
		}
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
			// Подписка устарела — удаляем
			if err := s.repo.DeletePushSubscription(ctx, sub.ID); err != nil {
				log.Warn().Err(err).Uint("sub_id", sub.ID).Msg("webpush: failed to delete stale subscription")
			}
		} else if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			log.Warn().Int("status", resp.StatusCode).Uint("user_id", userID).Msg("webpush: unexpected response status")
		}
	}
	return nil
}

// sendWebSocketNotification отправляет уведомление через WebSocket
func (s *NotificationService) sendWebSocketNotification(ctx context.Context, userID uint, notification *Notification) {
	roomID := fmt.Sprintf("user:%d", userID)

	notificationData := map[string]any{
		"type":         string(notification.Type),
		"id":           notification.ID,
		"title":        notification.Title,
		"body":         notification.Body,
		"link":         notification.Link,
		"created_at":   notification.CreatedAt.Format(time.RFC3339),
		"unread_count": s.getUnreadCount(ctx, userID),
	}

	data, err := json.Marshal(notificationData)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("Failed to marshal notification")
		return
	}

	s.hub.BroadcastToRoom(roomID, data)
}

// getUnreadCount возвращает количество непрочитанных уведомлений (с TTL-кэшем, P-M6).
func (s *NotificationService) getUnreadCount(ctx context.Context, userID uint) int {
	s.unreadMu.Lock()
	if e, ok := s.unreadCache[userID]; ok && time.Now().Before(e.expires) {
		s.unreadMu.Unlock()
		return e.count
	}
	s.unreadMu.Unlock()

	count, err := s.repo.CountUnread(ctx, userID)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("getUnreadCount: failed")
		count = 0
	}

	s.unreadMu.Lock()
	// Lazy sweep (P-2): записи живут 30с и удаляются при Create/MarkAsRead;
	// периодически чистим истёкшие, чтобы map не росла с числом активных юзеров.
	if len(s.unreadCache) > 512 {
		now := time.Now()
		for uid, e := range s.unreadCache {
			if now.After(e.expires) {
				delete(s.unreadCache, uid)
			}
		}
	}
	s.unreadCache[userID] = unreadEntry{count: int(count), expires: time.Now().Add(unreadCacheTTL)}
	s.unreadMu.Unlock()
	return int(count)
}

// invalidateUnreadCount сбрасывает кэш счётчика пользователя (Create/MarkAsRead).
func (s *NotificationService) invalidateUnreadCount(userID uint) {
	s.unreadMu.Lock()
	delete(s.unreadCache, userID)
	s.unreadMu.Unlock()
}

// incrementUnreadCount инкрементирует закэшированный unread-счётчик, если он
// жив; иначе инвалидирует (следующий getUnreadCount пересчитает из БД, включив
// только что созданное уведомление). L-1 (pass 40): убирает COUNT из БД на
// каждое создание уведомления, сохраняя точность WS-payload.
func (s *NotificationService) incrementUnreadCount(userID uint) {
	s.unreadMu.Lock()
	defer s.unreadMu.Unlock()
	if e, ok := s.unreadCache[userID]; ok && time.Now().Before(e.expires) {
		e.count++
		// Продлеваем TTL — запись остаётся актуальной после инкремента.
		e.expires = time.Now().Add(unreadCacheTTL)
		s.unreadCache[userID] = e
		return
	}
	delete(s.unreadCache, userID)
}

// GetByUser возвращает уведомления пользователя с пагинацией
func (s *NotificationService) GetByUser(ctx context.Context, userID uint, page, perPage int) ([]Notification, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 20
	}

	offset := (page - 1) * perPage
	return s.repo.ListByUser(ctx, userID, offset, perPage)
}

// MarkAsRead помечает уведомление как прочитанное
func (s *NotificationService) MarkAsRead(ctx context.Context, userID, notificationID uint) error {
	updated, err := s.repo.MarkAsRead(ctx, userID, notificationID)
	if err != nil {
		return err
	}
	if !updated {
		return fmt.Errorf("notification not found")
	}
	// Сброс кэша счётчика (P-M6).
	s.invalidateUnreadCount(userID)
	return nil
}

// MarkAllAsRead помечает все уведомления пользователя как прочитанные
func (s *NotificationService) MarkAllAsRead(ctx context.Context, userID uint) error {
	err := s.repo.MarkAllAsRead(ctx, userID)
	if err != nil {
		return err
	}
	s.invalidateUnreadCount(userID)
	return nil
}

// GetUnreadCount возвращает количество непрочитанных уведомлений
func (s *NotificationService) GetUnreadCount(ctx context.Context, userID uint) int {
	return s.getUnreadCount(ctx, userID)
}

// SendTimeWarning отправляет предупреждение о таймере
func (s *NotificationService) SendTimeWarning(ctx context.Context, userID uint, passingID uint, remainingSeconds int) error {
	title := i18n.T("notif.time_warning_title")
	message := i18n.TF("notif.time_warning_body", remainingSeconds)
	url := fmt.Sprintf("/game/%d", passingID)

	err := s.Create(ctx, userID, NotificationTypeTimeWarning, title, message, url)
	if err != nil {
		return err
	}

	// Отправляем SSE-уведомление
	if s.sseMgr != nil {
		gameID, err := s.repo.GetGamePassingGameID(ctx, passingID)
		if err == nil && gameID != 0 {
			s.sseMgr.Broadcast(gameID, "time_warning", map[string]any{
				"game_id":           gameID,
				"passing_id":        passingID,
				"remaining_seconds": remainingSeconds,
				"remaining_minutes": remainingSeconds / 60,
			})
		}
	}

	return nil
}

// SendTimeExpired отправляет уведомление об истечении времени
func (s *NotificationService) SendTimeExpired(ctx context.Context, userID uint, passingID uint) error {
	title := i18n.T("notif.time_expired_title")
	message := i18n.T("notif.time_expired_body")
	url := fmt.Sprintf("/game/%d", passingID)

	return s.Create(ctx, userID, NotificationTypeTimeExpired, title, message, url)
}
