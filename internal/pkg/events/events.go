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
	// Game events
	GameCreated       EventType = "game.created"
	GamePublished     EventType = "game.published"
	GameFinished      EventType = "game.finished"
	GameForceFinished EventType = "game.force_finished"
	GameDisqualified  EventType = "game.disqualified"

	// Passing events
	PassingApplied    EventType = "passing.applied"
	PassingAccepted   EventType = "passing.accepted"
	PassingRejected   EventType = "passing.rejected"
	PassingStarted    EventType = "passing.started"
	PassingFinished   EventType = "passing.finished"

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
	queueDepth    int64 // атомарный счётчик глубины очереди
	droppedEvents int64 // атомарный счётчик сброшенных событий
}

type job struct {
	handler Handler
	event   Event
}

// NewBus создаёт новый Bus с пулом воркеров (по умолчанию runtime.NumCPU).
func NewBus() *Bus {
	b := &Bus{
		handlers: make(map[EventType][]Handler),
		queue:    make(chan job, 256),
	}
	b.startWorkers(runtime.NumCPU())
	return b
}

// NewBusWithWorkers создаёт Bus с указанным количеством воркеров.
func NewBusWithWorkers(workers int) *Bus {
	b := &Bus{
		handlers: make(map[EventType][]Handler),
		queue:    make(chan job, 256),
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
				b.safeCall(j.handler, j.event)
			}
		}()
	}
}

// Stop завершает работу воркеров (ожидает завершения всех задач в очереди).
func (b *Bus) Stop() {
	close(b.queue)
	b.workerWg.Wait()
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
			atomic.AddInt64(&b.queueDepth, 1)
		default:
			atomic.AddInt64(&b.droppedEvents, 1)
			log.Warn().Str("event_type", string(event.Type)).Msg("events: queue full, dropping event")
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
