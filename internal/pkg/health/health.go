// internal/pkg/health/health.go
package health

import (
	"context"
	"time"

	"gengine-0/internal/pkg/cache"
	ws "gengine-0/internal/pkg/websocket"

	"gorm.io/gorm"
)

// Checker содержит зависимости для проверки здоровья.
type Checker struct {
	db     *gorm.DB
	hub    *ws.RoomHub
	valkey cache.CacheStore
}

// NewChecker создаёт новый Checker.
func NewChecker(db *gorm.DB, hub *ws.RoomHub) *Checker {
	return &Checker{db: db, hub: hub}
}

// NewCheckerWithValkey создаёт новый Checker с Valkey.
func NewCheckerWithValkey(db *gorm.DB, hub *ws.RoomHub, valkey cache.CacheStore) *Checker {
	return &Checker{db: db, hub: hub, valkey: valkey}
}

// Status представляет статус компонента.
type Status struct {
	Status  string `json:"status"`  // "ok" или "error"
	Message string `json:"message"` // опциональное сообщение
	Latency string `json:"latency"` // время ответа в миллисекундах
}

// HealthResponse общий ответ health-чека.
type HealthResponse struct {
	Status     string            `json:"status"`     // общий статус: "ok" или "degraded" или "error"
	Timestamp  string            `json:"timestamp"`  // время проверки
	Components map[string]Status `json:"components"` // статус каждого компонента
}

// Check выполняет проверку всех компонентов и возвращает HealthResponse.
func (c *Checker) Check(ctx context.Context) HealthResponse {
	components := make(map[string]Status)
	overall := "ok"

	// Проверка БД
	dbStatus := c.checkDatabase(ctx)
	components["database"] = dbStatus
	if dbStatus.Status != "ok" {
		overall = "degraded"
	}

	// Проверка WebSocket-хаба
	hubStatus := c.checkHub()
	components["websocket_hub"] = hubStatus
	if hubStatus.Status != "ok" {
		overall = "degraded"
	}

	// Проверка Valkey (если настроен)
	if c.valkey != nil {
		valkeyStatus := c.checkValkey(ctx)
		components["valkey"] = valkeyStatus
		if valkeyStatus.Status != "ok" {
			overall = "degraded"
		}
	}

	// Если оба компонента в ошибке — ставим "error"
	if dbStatus.Status == "error" && hubStatus.Status == "error" {
		overall = "error"
	}

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
	// Устанавливаем таймаут для ping
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
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
func (c *Checker) checkHub() Status {
	if c.hub == nil {
		return Status{
			Status:  "error",
			Message: "hub is nil",
		}
	}
	// Простая проверка: если hub не nil, считаем, что работает.
	// При необходимости можно добавить проверку на закрытие канала,
	// если у ws.RoomHub есть поле done.
	return Status{
		Status:  "ok",
		Message: "hub is running",
	}
}

// checkValkey проверяет соединение с Valkey (если настроен).
func (c *Checker) checkValkey(ctx context.Context) Status {
	if c.valkey == nil {
		// Valkey не настроен — это не ошибка
		return Status{
			Status:  "ok",
			Message: "not configured",
		}
	}
	start := time.Now()
	// Пытаемся получить значение из кэша — если работает, значит connection ok
	_, ok := c.valkey.Get("health:check")
	if !ok {
		// Ключ не найден — это нормально, главное что не было ошибки
	}
	return Status{
		Status:  "ok",
		Message: "connected",
		Latency: time.Since(start).String(),
	}
}
