// internal/pkg/realtimebus/bus_test.go
package realtimebus

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return mr, client
}

func TestValkeyBus_PublishSubscribe(t *testing.T) {
	_, client := newTestClient(t)
	bus := NewValkeyBus(client)
	defer bus.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	received := make(chan []byte, 4)
	bus.Subscribe(ctx, WSChannel, func(ch string, payload []byte) {
		received <- payload
	})

	// Даём подписке стартовать.
	time.Sleep(100 * time.Millisecond)

	require.NoError(t, bus.Publish(ctx, WSChannel, []byte("hello")))
	require.NoError(t, bus.Publish(ctx, WSChannel, []byte("world")))

	select {
	case msg := <-received:
		assert.Equal(t, "hello", string(msg))
	case <-ctx.Done():
		t.Fatal("timed out waiting for first message")
	}
	select {
	case msg := <-received:
		assert.Equal(t, "world", string(msg))
	case <-ctx.Done():
		t.Fatal("timed out waiting for second message")
	}
}

func TestValkeyBus_MultipleChannels(t *testing.T) {
	_, client := newTestClient(t)
	bus := NewValkeyBus(client)
	defer bus.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var mu sync.Mutex
	got := make(map[string]int)
	bus.Subscribe(ctx, WSChannel, func(ch string, _ []byte) {
		mu.Lock()
		got[ch]++
		mu.Unlock()
	})
	bus.Subscribe(ctx, SSEChannel, func(ch string, _ []byte) {
		mu.Lock()
		got[ch]++
		mu.Unlock()
	})

	// Ждём готовности подписки (Subscribe асинхронный), затем публикуем.
	// Первые публикации могут теряться, пока redis-подписка не установлена
	// (асинхронный Subscribe) — ретраим, как это делал бы клиент на reconnect.
	require.Eventually(t, func() bool {
		b, _ := bus.(*valkeyBus)
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.started && len(b.handlers) == 2
	}, 3*time.Second, 10*time.Millisecond)

	var lastErr error
	require.Eventually(t, func() bool {
		for range 5 {
			if err := bus.Publish(ctx, WSChannel, []byte("1")); err != nil {
				lastErr = err
			}
			if err := bus.Publish(ctx, SSEChannel, []byte("2")); err != nil {
				lastErr = err
			}
			mu.Lock()
			both := got[WSChannel] >= 1 && got[SSEChannel] >= 1
			mu.Unlock()
			if both {
				return true
			}
			time.Sleep(50 * time.Millisecond)
		}
		return false
	}, 3*time.Second, 50*time.Millisecond)
	if lastErr != nil {
		t.Fatalf("publish failed: %v", lastErr)
	}
}

func TestValkeyBus_TwoBuses_TwoInstances(t *testing.T) {
	// Имитация двух инстансов, подключённых к одному Valkey.
	mr := miniredis.RunT(t)
	client1 := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	client2 := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client1.Close() })
	t.Cleanup(func() { _ = client2.Close() })

	bus1 := NewValkeyBus(client1)
	bus2 := NewValkeyBus(client2)
	defer bus1.Close()
	defer bus2.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got2 := make(chan []byte, 2)
	bus1.Subscribe(ctx, WSChannel, func(_ string, _ []byte) {})
	bus2.Subscribe(ctx, WSChannel, func(_ string, payload []byte) {
		got2 <- payload
	})

	time.Sleep(150 * time.Millisecond)
	require.NoError(t, bus1.Publish(ctx, WSChannel, []byte("cross-instance")))

	select {
	case msg := <-got2:
		assert.Equal(t, "cross-instance", string(msg))
	case <-ctx.Done():
		t.Fatal("instance 2 did not receive cross-instance message")
	}
}

func TestValkeyBus_NoopBus(t *testing.T) {
	bus := NoopBus()
	ctx := context.Background()
	require.NoError(t, bus.Publish(ctx, WSChannel, []byte("x")))
	bus.Subscribe(ctx, WSChannel, func(_ string, _ []byte) {})
	bus.Close() // no panic
}

func TestValkeyBus_CloseStopsSubscriber(t *testing.T) {
	_, client := newTestClient(t)
	bus := NewValkeyBus(client)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	count := 0
	var mu sync.Mutex
	bus.Subscribe(ctx, WSChannel, func(_ string, _ []byte) {
		mu.Lock()
		count++
		mu.Unlock()
	})
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, bus.Publish(ctx, WSChannel, []byte("1")))
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return count == 1
	}, 3*time.Second, 10*time.Millisecond)

	bus.Close()
	// После Close новые подписки игнорируются, publish не приводит к обработке.
	time.Sleep(50 * time.Millisecond)
	require.NoError(t, bus.Publish(ctx, WSChannel, []byte("2")))
	time.Sleep(150 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, count, "no messages processed after Close")
}
