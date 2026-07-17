// internal/pkg/events/events.go
package events

import (
	"context"
	"sync"
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
	Type   EventType
	Data   map[string]interface{}
	Ctx    context.Context
	Timed  bool
}

// Handler определяет функцию-обработчик события.
type Handler func(event Event)

// Bus обеспечивает publish/subscribe для domain events.
type Bus struct {
	mu      sync.RWMutex
	handlers map[EventType][]Handler
}

// NewBus создаёт новый Bus.
func NewBus() *Bus {
	return &Bus{
		handlers: make(map[EventType][]Handler),
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

// Publish отправляет событие всем подписчикам.
func (b *Bus) Publish(event Event) {
	b.mu.RLock()
	handlers := b.handlers[event.Type]
	allHandlers := b.handlers["*"]
	b.mu.RUnlock()

	for _, h := range handlers {
		go b.safeCall(h, event)
	}
	for _, h := range allHandlers {
		go b.safeCall(h, event)
	}
}

// safeCall вызывает обработчик с recover для предотвращения panic.
func (b *Bus) safeCall(handler Handler, event Event) {
	defer func() {
		if r := recover(); r != nil {
			// Логирование panic в обработчике событий
			// Здесь можно добавить логгер
			_ = r
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
	// Применяем middleware в обратном порядке
	for i := len(mb.middleware) - 1; i >= 0; i-- {
		handler = mb.middleware[i](handler)
	}
	mb.Bus.Subscribe(eventType, handler)
}
