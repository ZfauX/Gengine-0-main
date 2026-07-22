// internal/pkg/events/events.go
package events

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog/log"
)

// EventType определяет тип события.
type EventType string

const (
	eventBusQueueSize = 256

	// Game events
	GameCreated       EventType = "game.created"
	GamePublished     EventType = "game.published"
	GameFinished      EventType = "game.finished"
	GameForceFinished EventType = "game.force_finished"
	GameDisqualified  EventType = "game.disqualified"

	// Passing events
	PassingApplied  EventType = "passing.applied"
	PassingAccepted EventType = "passing.accepted"
	PassingRejected EventType = "passing.rejected"
	PassingStarted  EventType = "passing.started"
	PassingFinished EventType = "passing.finished"

	// User events
	UserRegistered EventType = "user.registered"
	UserLoggedIn   EventType = "user.logged_in"
)

// Event представляет domain event.
type Event struct {
	Type  EventType
	Data  map[string]any
	Ctx   context.Context
	Timed bool
}

// Handler определяет функцию-обработчик события.
type Handler func(event Event)

// Bus обеспечивает publish/subscribe для domain events с worker pool.
type Bus struct {
	mu       sync.RWMutex
	handlers map[EventType][]Handler
	queue    chan job
	workerWg sync.WaitGroup

	// Метрики
	queueSize     int64 // текущая глубина очереди (публикации - обработки)
	processed     int64 // количество обработанных событий
	droppedEvents int64 // количество сброшенных событий при переполнении
	totalEvents   int64 // всего событий, переданных в Publish
}

type job struct {
	handler Handler
	event   Event
}

// NewBus создаёт новый Bus с пулом воркеров (по умолчанию runtime.NumCPU).
func NewBus() *Bus {
	b := &Bus{
		handlers: make(map[EventType][]Handler),
		queue:    make(chan job, eventBusQueueSize),
	}
	b.startWorkers(runtime.NumCPU())
	return b
}

// NewBusWithWorkers создаёт Bus с указанным количеством воркеров.
func NewBusWithWorkers(workers int) *Bus {
	b := &Bus{
		handlers: make(map[EventType][]Handler),
		queue:    make(chan job, eventBusQueueSize),
	}
	b.startWorkers(workers)
	return b
}

func (b *Bus) startWorkers(n int) {
	for range n {
		b.workerWg.Add(1)
		go func() {
			defer b.workerWg.Done()
			for j := range b.queue {
				atomic.AddInt64(&b.queueSize, -1)
				b.safeCall(j.handler, j.event)
				atomic.AddInt64(&b.processed, 1)
			}
		}()
	}
}

// Stop завершает работу воркеров (ожидает завершения всех задач в очереди).
func (b *Bus) Stop() {
	close(b.queue)
	b.workerWg.Wait()
}

// Metrics возвращает текущие метрики работы Bus.
type BusMetrics struct {
	QueueSize     int64 `json:"queue_size"`
	Processed     int64 `json:"processed"`
	DroppedEvents int64 `json:"dropped_events"`
	TotalEvents   int64 `json:"total_events"`
}

// GetMetrics возвращает копию текущих метрик.
func (b *Bus) GetMetrics() BusMetrics {
	return BusMetrics{
		QueueSize:     atomic.LoadInt64(&b.queueSize),
		Processed:     atomic.LoadInt64(&b.processed),
		DroppedEvents: atomic.LoadInt64(&b.droppedEvents),
		TotalEvents:   atomic.LoadInt64(&b.totalEvents),
	}
}

// Subscribe регистрирует обработчик для определённого типа событий.
func (b *Bus) Subscribe(eventType EventType, handler Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[eventType] = append(b.handlers[eventType], handler)
}

// SubscribeAll регистрирует обработчик для всех событий.
func (b *Bus) SubscribeAll(handler Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers["*"] = append(b.handlers["*"], handler)
}

// Publish отправляет событие всем подписчикам через worker pool.
func (b *Bus) Publish(event Event) {
	b.mu.RLock()
	handlers := b.handlers[event.Type]
	allHandlers := b.handlers["*"]
	b.mu.RUnlock()

	publish := func(h Handler) {
		select {
		case b.queue <- job{handler: h, event: event}:
			atomic.AddInt64(&b.queueSize, 1)
			atomic.AddInt64(&b.totalEvents, 1)
		default:
			atomic.AddInt64(&b.droppedEvents, 1)
			atomic.AddInt64(&b.totalEvents, 1)
			log.Warn().Int64("dropped", atomic.LoadInt64(&b.droppedEvents)).Str("event_type", string(event.Type)).Msg("events: queue full, dropping event")
			go b.safeCall(h, event)
		}
	}

	for _, h := range handlers {
		publish(h)
	}
	for _, h := range allHandlers {
		publish(h)
	}
}

// safeCall вызывает обработчик с recover для предотвращения panic.
func (b *Bus) safeCall(handler Handler, event Event) {
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Str("event_type", string(event.Type)).Msg("events: panic in handler")
		}
	}()
	handler(event)
}

// MiddlewareBus расширяет Bus middleware-цепочкой.
type MiddlewareBus struct {
	*Bus
	middleware []Middleware
}

// Middleware определяет функцию-обёртку для обработчиков.
type Middleware func(Handler) Handler

// NewMiddlewareBus создаёт новый Bus с middleware.
func NewMiddlewareBus() *MiddlewareBus {
	return &MiddlewareBus{
		Bus:        NewBus(),
		middleware: make([]Middleware, 0),
	}
}

// Use добавляет middleware.
func (mb *MiddlewareBus) Use(middleware Middleware) {
	mb.middleware = append(mb.middleware, middleware)
}

// SubscribeWithMiddleware регистрирует обработчик с middleware.
func (mb *MiddlewareBus) Subscribe(eventType EventType, handler Handler) {
	for i := len(mb.middleware) - 1; i >= 0; i-- {
		handler = mb.middleware[i](handler)
	}
	mb.Bus.Subscribe(eventType, handler)
}

// GetMetrics — делегирование метрик родительскому Bus.
func (mb *MiddlewareBus) GetMetrics() BusMetrics {
	return mb.Bus.GetMetrics()
}
