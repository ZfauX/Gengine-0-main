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
	"github.com/rs/zerolog/log"
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
	// pubsub — активная подписка (nil до первого runOnce и после закрытия).
	pubsub *redis.PubSub
	// subscribed — true, когда Redis подтвердил SUBSCRIBE (первый runOnce).
	subscribed bool
}

// subscribeReadyTimeout — сколько ждать подтверждения подписки Redis.
// Ожидание делает Subscribe синхронным: после возврата Subscribe первые
// публикации НЕ теряются (асинхронная подписка — источник флейков тестов
// и потери первых сообщений на старте инстанса).
const subscribeReadyTimeout = 5 * time.Second

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

// Subscribe регистрирует обработчик канала и (при первом вызове) запускает
// горутину чтения с авто-reconnect. Первый вызов дожидается подтверждения
// подписки Redis (SUBSCRIBE ack), поэтому после возврата Subscribe сообщения
// доставляются без потерь. Последующие вызовы (новые каналы) динамически
// добавляются к уже активной подписке.
//
// Почему динамически: в main.go hub.SetPubSub (WSChannel) и
// SSEMgr.SetPubSub (SSEChannel) вызываются последовательно. Если создавать
// подписку только при первом Subscribe, второй канал никогда не попадёт в
// активную подписку (SSE cross-instance рассылка молча не работала).
func (b *valkeyBus) Subscribe(ctx context.Context, channel string, handler func(channel string, payload []byte)) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	_, existed := b.handlers[channel]
	b.handlers[channel] = handler
	start := !b.started
	b.started = true
	ps := b.pubsub
	b.mu.Unlock()

	if start {
		go b.run(ctx)
		// Ждём установки подписки (первый runOnce подтвердит SUBSCRIBE).
		b.waitReady(ctx)
		return
	}
	// Канал уже был зарегистрирован — активная подписка уже включает его.
	if existed {
		return
	}
	// Новый канал: динамически добавляем к активной подписке. Если подписки
	// ещё нет (reconnect в процессе) — канал уже в handlers, runOnce при
	// переподписке захватит его.
	if ps != nil {
		if err := ps.Subscribe(ctx, channel); err != nil {
			log.Warn().Err(err).Str("channel", channel).Msg("realtimebus: dynamic subscribe failed")
		}
	}
}

// waitReady блокирует, пока runOnce не подтвердит подписку (или таймаут/close).
func (b *valkeyBus) waitReady(ctx context.Context) {
	timeout := time.NewTimer(subscribeReadyTimeout)
	defer timeout.Stop()
	for {
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			return
		}
		ready := b.subscribed
		b.mu.Unlock()
		if ready {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-b.closeCh:
			return
		case <-timeout.C:
			log.Warn().Msg("realtimebus: subscribe ready timeout (pub/sub may drop first messages)")
			return
		case <-time.After(5 * time.Millisecond):
		}
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
	pubsub := b.client.Subscribe(ctx, channels...)
	b.pubsub = pubsub
	b.subscribed = false
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		if b.pubsub == pubsub {
			b.pubsub = nil
		}
		b.mu.Unlock()
		_ = pubsub.Close()
	}()

	// Синхронно ждём подтверждения SUBSCRIBE от Redis (go-redis Subscribe
	// возвращается до установки подписки). Пока не подтверждено — сообщения
	// могут теряться, и первая публикация уходит в никуда.
	if !b.waitSubscribeAck(ctx, pubsub) {
		return ctx.Err()
	}
	b.mu.Lock()
	b.subscribed = true
	b.mu.Unlock()

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

// waitSubscribeAck ждёт подтверждения подписки от Redis. go-redis подтверждает
// через *redis.Subscription (SUBSCRIBE ack) или *redis.Pong. Возвращает false
// при отмене контекста/закрытии шины.
func (b *valkeyBus) waitSubscribeAck(ctx context.Context, pubsub *redis.PubSub) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		case <-b.closeCh:
			return false
		default:
		}
		msg, err := pubsub.ReceiveTimeout(ctx, 2*time.Second)
		if err != nil {
			if ctx.Err() != nil || b.isClosed() {
				return false
			}
			log.Debug().Err(err).Msg("realtimebus: waiting subscribe ack")
			time.Sleep(20 * time.Millisecond)
			continue
		}
		switch m := msg.(type) {
		case *redis.Subscription:
			return true
		case *redis.Pong:
			// PING-ответ — подписка может ещё устанавливаться; продолжаем ждать.
			continue
		case *redis.Message:
			// Сообщение пришло до полного подтверждения — обрабатываем.
			b.mu.Lock()
			h, exists := b.handlers[m.Channel]
			b.mu.Unlock()
			if exists {
				h(m.Channel, []byte(m.Payload))
			}
		}
	}
}

func (b *valkeyBus) isClosed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}
