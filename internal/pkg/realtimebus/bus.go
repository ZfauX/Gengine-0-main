// Package realtimebus — cross-instance шина для real-time подсистем
// (WebSocket RoomHub и SSE SSEManager) на базе Valkey/Redis pub/sub.
//
// Проблема, которую решает: RoomHub и SSEManager держат подключения в памяти
// КОНКРЕТНОГО инстанса. При горизонтальном масштабировании (N инстансов за
// балансировщиком) сообщение, опубликованное на инстансе A, должно дойти до
// клиентов, подключённых к инстансам B и C. Пакет реализует надёжную
// (с авто-reconnect) публикацию/подписку поверх go-redis PubSub.
//
// Anti-эхо: каждое сообщение снабжается полем Origin (уникальный instanceID).
// Инстанс-отправитель рассылает локально сам и ПРОПУСКАЕТ своё сообщение,
// полученное обратно из подписки. Подписчики других инстансов рассылают его
// локальным клиентам. Так сообщение доставляется каждому клиенту ровно один раз.
package realtimebus

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// WSChannel — канал для WebSocket-сообщений.
const WSChannel = "gengine:ws"

// SSEChannel — канал для SSE-сообщений.
const SSEChannel = "gengine:sse"

// Bus — интерфейс pub/sub-шины. Реализации: ValkeyBus (на go-redis).
// NoopBus (без Valkey) может использоваться, когда шина не настроена.
type Bus interface {
	// Publish отправляет payload в канал. Синхронно; при ошибке — возвращает её
	// (вызывающий решает, fail-open/fail-closed). Context — таймаут/отмена.
	Publish(ctx context.Context, channel string, payload []byte) error
	// Subscribe регистрирует обработчик сообщений канала. Каждый инстанс
	// подписывается один раз на старте. Обработчик вызывается из горутины
	// шины; должен быть быстрым (без блокировок/долгих I/O).
	Subscribe(ctx context.Context, channel string, handler func(channel string, payload []byte))
	// Close останавливает все подписки и освобождает ресурсы.
	Close()
}

// noopBus — шина-заглушка (Valkey не настроен). Публикация — no-op успех,
// подписка — no-op. Позволяет держать код без ветвлений.
type noopBus struct{}

// NoopBus создаёт шину-заглушку.
func NoopBus() Bus { return &noopBus{} }

func (*noopBus) Publish(context.Context, string, []byte) error           { return nil }
func (*noopBus) Subscribe(context.Context, string, func(string, []byte)) {}
func (*noopBus) Close()                                                  {}

// reconnectBackoffMin/Max — диапазон экспоненциального backoff при reconnect.
const (
	reconnectBackoffMin = 500 * time.Millisecond
	reconnectBackoffMax = 30 * time.Second
)

// valkeyBus — реализация Bus на go-redis PubSub.
type valkeyBus struct {
	client *redis.Client

	mu       sync.Mutex
	handlers map[string]func(channel string, payload []byte)
	started  bool
	closed   bool
	closeCh  chan struct{}
}

// NewValkeyBus создаёт шину на клиенте go-redis. Один клиент на все подписки.
func NewValkeyBus(client *redis.Client) Bus {
	return &valkeyBus{
		client:   client,
		handlers: make(map[string]func(channel string, payload []byte)),
		closeCh:  make(chan struct{}),
	}
}

// Publish отправляет payload в канал.
func (b *valkeyBus) Publish(ctx context.Context, channel string, payload []byte) error {
	return b.client.Publish(ctx, channel, payload).Err()
}

// Subscribe регистрирует обработчик и (при первом вызове) запускает горутину
// чтения, которая автоматически переподписывается при обрыве соединения.
func (b *valkeyBus) Subscribe(ctx context.Context, channel string, handler func(channel string, payload []byte)) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.handlers[channel] = handler
	start := !b.started
	b.started = true
	b.mu.Unlock()
	if start {
		go b.run(ctx)
	}
}

// Close останавливает подписки.
func (b *valkeyBus) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	close(b.closeCh)
	b.mu.Unlock()
}

// run — цикл чтения подписок с reconnect. Завершается по ctx.Done() или Close().
func (b *valkeyBus) run(ctx context.Context) {
	backoff := reconnectBackoffMin
	for {
		if b.isClosed() || ctx.Err() != nil {
			return
		}
		err := b.runOnce(ctx)
		if err == nil || ctx.Err() != nil || b.isClosed() {
			return
		}
		// Обрыв соединения — переподписываемся с backoff.
		select {
		case <-ctx.Done():
			return
		case <-b.closeCh:
			return
		case <-time.After(backoff):
		}
		if backoff < reconnectBackoffMax {
			backoff *= 2
		}
	}
}

// runOnce подписывается на все каналы и читает сообщения до ошибки.
func (b *valkeyBus) runOnce(ctx context.Context) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	channels := make([]string, 0, len(b.handlers))
	for ch := range b.handlers {
		channels = append(channels, ch)
	}
	b.mu.Unlock()

	pubsub := b.client.Subscribe(ctx, channels...)
	defer func() { _ = pubsub.Close() }()

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-b.closeCh:
			return nil
		case msg, ok := <-ch:
			if !ok {
				return errors.New("realtimebus: pubsub channel closed")
			}
			b.mu.Lock()
			h, exists := b.handlers[msg.Channel]
			b.mu.Unlock()
			if exists {
				h(msg.Channel, []byte(msg.Payload))
			}
		}
	}
}

func (b *valkeyBus) isClosed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}
