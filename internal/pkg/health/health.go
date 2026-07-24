// internal/pkg/health/health.go
package health

import (
	"context"
	"fmt"
	"sync"
	"time"

	"gengine-0/internal/pkg/cache"
	"gengine-0/internal/pkg/email"
	ws "gengine-0/internal/pkg/websocket"

	"gorm.io/gorm"
)

// Checker содержит зависимости для проверки здоровья.
type Checker struct {
	db                   *gorm.DB
	hub                  *ws.RoomHub
	valkey               cache.CacheStore
	failedEmailThreshold int
	uploadsDir           string
}

// NewChecker создаёт новый Checker.
func NewChecker(db *gorm.DB, hub *ws.RoomHub) *Checker {
	return &Checker{db: db, hub: hub, failedEmailThreshold: 100}
}

// NewCheckerWithValkey создаёт новый Checker с Valkey.
func NewCheckerWithValkey(db *gorm.DB, hub *ws.RoomHub, valkey cache.CacheStore) *Checker {
	return &Checker{db: db, hub: hub, valkey: valkey}
}

// WithUploadsDir устанавливает путь к директории загрузок для проверки дискового пространства.
func (c *Checker) WithUploadsDir(dir string) *Checker {
	c.uploadsDir = dir
	return c
}

// Status представляет статус компонента.
type Status struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Latency string `json:"latency"`
}

// HealthResponse общий ответ health-чека.
type HealthResponse struct {
	Status     string            `json:"status"`
	Timestamp  string            `json:"timestamp"`
	Components map[string]Status `json:"components"`
}

const healthCheckTimeout = 5 * time.Second

// Check выполняет параллельную проверку всех компонентов.
func (c *Checker) Check(ctx context.Context) HealthResponse {
	components := make(map[string]Status)
	var mu sync.Mutex
	var wg sync.WaitGroup
	overall := "ok"

	checkCtx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()

	// Функция для параллельного выполнения проверок
	check := func(name string, fn func(context.Context) Status) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status := fn(checkCtx)
			mu.Lock()
			components[name] = status
			if status.Status != "ok" {
				overall = "degraded"
			}
			mu.Unlock()
		}()
	}

	check("database", c.checkDatabase)
	check("websocket_hub", c.checkHub)

	if c.valkey != nil {
		check("valkey", c.checkValkey)
	}

	check("email_queue", c.checkEmailQueue)

	if c.uploadsDir != "" {
		check("disk_space", c.checkDiskSpace)
	}

	wg.Wait()

	// Если оба компонента в ошибке — ставим "error"
	mu.Lock()
	if components["database"].Status == "error" && components["websocket_hub"].Status == "error" {
		overall = "error"
	}
	mu.Unlock()

	return HealthResponse{
		Status:     overall,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		Components: components,
	}
}

// checkDatabase проверяет соединение с БД через ping.
func (c *Checker) checkDatabase(ctx context.Context) Status {
	start := time.Now()
	sqlDB, err := c.db.DB()
	if err != nil {
		return Status{
			Status:  "error",
			Message: "failed to get sql.DB: " + err.Error(),
			Latency: time.Since(start).String(),
		}
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		return Status{
			Status:  "error",
			Message: "ping failed: " + err.Error(),
			Latency: time.Since(start).String(),
		}
	}
	return Status{
		Status:  "ok",
		Message: "connected",
		Latency: time.Since(start).String(),
	}
}

// checkHub проверяет, что WebSocket-хаб работает (не закрыт).
func (c *Checker) checkHub(ctx context.Context) Status {
	if c.hub == nil {
		return Status{
			Status:  "error",
			Message: "hub is nil",
		}
	}
	if c.hub.IsStopped() {
		return Status{
			Status:  "error",
			Message: "hub is stopped",
		}
	}
	return Status{
		Status:  "ok",
		Message: "hub is running",
	}
}

// checkValkey проверяет соединение с Valkey (если настроен).
func (c *Checker) checkValkey(ctx context.Context) Status {
	if c.valkey == nil {
		return Status{
			Status:  "ok",
			Message: "not configured",
		}
	}
	start := time.Now()
	// Используем Ping напрямую, если CacheStore её поддерживает
	if vc, ok := c.valkey.(*cache.ValkeyCache); ok {
		if vc.IsAvailable() {
			return Status{
				Status:  "ok",
				Message: "connected",
				Latency: time.Since(start).String(),
			}
		}
		return Status{
			Status:  "error",
			Message: "valkey connection failed",
			Latency: time.Since(start).String(),
		}
	}
	// Для неизвестных реализаций — считаем ok
	return Status{
		Status:  "ok",
		Message: "unknown cache type, assumed ok",
	}
}

// checkEmailQueue проверяет количество failed email в очереди.
func (c *Checker) checkEmailQueue(ctx context.Context) Status {
	start := time.Now()

	var failedCount int64
	if err := c.db.WithContext(ctx).Model(&email.QueuedEmail{}).Where("status = ?", "failed").Count(&failedCount).Error; err != nil {
		return Status{
			Status:  "error",
			Message: "failed to check email queue: " + err.Error(),
			Latency: time.Since(start).String(),
		}
	}

	// Используем приблизительную оценку: если строк > порога — degraded
	if failedCount > int64(c.failedEmailThreshold) {
		return Status{
			Status:  "degraded",
			Message: fmt.Sprintf("high number of failed emails: %d", failedCount),
			Latency: time.Since(start).String(),
		}
	}

	return Status{
		Status:  "ok",
		Message: fmt.Sprintf("failed emails: %d", failedCount),
		Latency: time.Since(start).String(),
	}
}

// checkDiskSpace проверяет свободное место на диске для uploads.
func (c *Checker) checkDiskSpace(ctx context.Context) Status {
	start := time.Now()

	dir := c.uploadsDir

	freeBytes, err := freeDiskSpace(dir)
	if err != nil {
		return Status{
			Status:  "error",
			Message: "failed to check disk space: " + err.Error(),
			Latency: time.Since(start).String(),
		}
	}

	freeMB := freeBytes / (1024 * 1024)
	if freeMB < 100 {
		return Status{
			Status:  "degraded",
			Message: fmt.Sprintf("low disk space: %d MB free", freeMB),
			Latency: time.Since(start).String(),
		}
	}

	return Status{
		Status:  "ok",
		Message: fmt.Sprintf("disk space: %d MB free", freeMB),
		Latency: time.Since(start).String(),
	}
}
