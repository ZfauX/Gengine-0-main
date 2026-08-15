// internal/pkg/audit/audit.go
package audit

import (
	"context"
	"strconv"
	"sync"
	"time"

	"gengine-0/internal/pkg/metrics"
	"gengine-0/internal/pkg/sqlutil"

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
	// TotalCount — P-09 (pass 42): COUNT(*) OVER() из того же запроса.
	TotalCount int64 `json:"-"`
}

// auditBatchSize (M8, PASS-19): батч-INSERT асинхронного воркера аудита.
const auditBatchSize = 50

// auditFlushInterval — максимальная задержка записи батча (M8).
const auditFlushInterval = 500 * time.Millisecond

// Service записывает и читает события аудита.
// M8 (PASS-19): Log() шлёт запись в канал (non-blocking), асинхронный воркер
// делает батч-INSERT (ранее синхронный INSERT на каждый лог = 1 RTT на
// горячем пути админ-действий). Ошибки воркера логируются + метрика.
type Service struct {
	DB *gorm.DB

	mu       sync.Mutex
	queue    chan Entry
	closed   bool
	done     chan struct{}
	workerWg sync.WaitGroup
}

// NewService создаёт новый Service и запускает асинхронный воркер записи.
func NewService(db *gorm.DB) *Service {
	s := &Service{
		DB:    db,
		queue: make(chan Entry, 512),
		done:  make(chan struct{}),
	}
	s.workerWg.Add(1)
	go s.worker()
	return s
}

// Stop завершает воркер и дожидается записи накопленных событий.
// Вызывается при graceful shutdown (main.go).
func (s *Service) Stop() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.done)
	s.mu.Unlock()
	s.workerWg.Wait()
}

// Log создаёт запись аудита. Non-blocking: при переполнении очереди запись
// дропается с метрикой (не прерывает бизнес-логику).
func (s *Service) Log(userID uint, action, objectType string, objectID uint, details string) {
	e := Entry{
		UserID:     userID,
		Action:     action,
		ObjectType: objectType,
		ObjectID:   objectID,
		Details:    details,
	}
	select {
	case s.queue <- e:
	default:
		metrics.AuditFailuresTotal.Inc()
		log.Warn().Str("action", action).Msg("audit: queue full, dropping entry")
	}
}

// worker читает очередь и пишет батчами.
func (s *Service) worker() {
	defer s.workerWg.Done()
	var batch []Entry
	ticker := time.NewTicker(auditFlushInterval)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := s.DB.Create(&batch).Error; err != nil {
			metrics.AuditFailuresTotal.Inc()
			log.Error().Err(err).Int("n", len(batch)).Msg("audit: failed to flush batch")
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-s.done:
			// Дреним остаток очереди (best-effort).
			for {
				select {
				case e := <-s.queue:
					batch = append(batch, e)
				default:
					flush()
					return
				}
			}
		case e := <-s.queue:
			batch = append(batch, e)
			if len(batch) >= auditBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
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
// P-09 (pass 42): COUNT(*) OVER() в одном запросе — раньше отдельный Count
// делал второй round-trip на каждый просмотр админ-страницы аудита.
func (s *Service) List(ctx context.Context, userIDStr, action, query string, page, perPage int) ([]EntryWithUser, int64, error) {
	base := s.DB.WithContext(ctx).Table("audit_logs").
		Joins("LEFT JOIN users ON users.id = audit_logs.user_id")

	if userIDStr != "" {
		if id, err := strconv.Atoi(userIDStr); err == nil {
			base = base.Where("audit_logs.user_id = ?", id)
		}
	}
	if action != "" {
		base = base.Where("audit_logs.action = ?", action)
	}
	if query != "" {
		like := sqlutil.BuildLikePattern(query)
		base = base.Where("(users.name ILIKE ? OR users.email ILIKE ?)", like, like)
	}

	var rows []EntryWithUser
	offset := (page - 1) * perPage
	err := base.
		Select("audit_logs.id, audit_logs.created_at, audit_logs.user_id, users.name AS user_name, audit_logs.action, audit_logs.object_type, audit_logs.object_id, audit_logs.details, COUNT(*) OVER() AS total_count").
		Order("audit_logs.created_at DESC").
		Offset(offset).
		Limit(perPage).
		Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}

	var total int64
	if len(rows) > 0 {
		total = rows[0].TotalCount
	}
	return rows, total, nil
}
