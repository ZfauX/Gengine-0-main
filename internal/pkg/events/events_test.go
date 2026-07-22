package events

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBus_PublishSubscribe(t *testing.T) {
	b := NewBusWithWorkers(2)
	defer b.Stop()

	var called atomic.Int32
	b.Subscribe(GameCreated, func(event Event) {
		called.Add(1)
	})

	b.Publish(Event{Type: GameCreated, Data: map[string]any{"id": 1}})
	b.Publish(Event{Type: GameCreated, Data: map[string]any{"id": 2}})

	require.Eventually(t, func() bool {
		return called.Load() == 2
	}, 2*time.Second, 50*time.Millisecond)
}

func TestBus_PublishSubscribeAll(t *testing.T) {
	b := NewBusWithWorkers(2)
	defer b.Stop()

	var gameCalls, allCalls atomic.Int32
	b.Subscribe(GameCreated, func(event Event) {
		gameCalls.Add(1)
	})
	b.SubscribeAll(func(event Event) {
		allCalls.Add(1)
	})

	b.Publish(Event{Type: GameCreated})
	b.Publish(Event{Type: UserRegistered})

	require.Eventually(t, func() bool {
		return gameCalls.Load() == 1 && allCalls.Load() == 2
	}, 2*time.Second, 50*time.Millisecond)
}

func TestBus_PanicRecovery(t *testing.T) {
	b := NewBusWithWorkers(1)
	defer b.Stop()

	var called atomic.Int32
	b.Subscribe(GameCreated, func(event Event) {
		panic("test panic")
	})
	b.Subscribe(GameCreated, func(event Event) {
		called.Add(1)
	})

	b.Publish(Event{Type: GameCreated})

	// Второй обработчик должен выполниться несмотря на panic в первом
	require.Eventually(t, func() bool {
		return called.Load() == 1
	}, 2*time.Second, 50*time.Millisecond)
}

func TestBus_Stop(t *testing.T) {
	b := NewBusWithWorkers(2)

	var started atomic.Int32
	b.Subscribe(GameCreated, func(event Event) {
		started.Add(1)
		time.Sleep(200 * time.Millisecond)
	})

	// Заполняем очередь
	for range 10 {
		b.Publish(Event{Type: GameCreated})
	}
	// Ждём начала обработки
	require.Eventually(t, func() bool {
		return started.Load() > 0
	}, 2*time.Second, 50*time.Millisecond)

	// Stop должен завершиться без зависания
	done := make(chan struct{})
	go func() {
		b.Stop()
		close(done)
	}()
	select {
	case <-done:
		// OK: Stop завершился
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() did not complete within timeout")
	}
}

func TestBus_NoHandlers(t *testing.T) {
	b := NewBusWithWorkers(1)
	defer b.Stop()

	// Не должно паниковать
	b.Publish(Event{Type: "nonexistent"})
	// Ждём завершения обработки (или отсутствия ошибок)
	require.Eventually(t, func() bool {
		return true
	}, 500*time.Millisecond, 50*time.Millisecond)
}

func TestBus_EventWithContext(t *testing.T) {
	b := NewBusWithWorkers(1)
	defer b.Stop()

	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "value")
	var gotCtx context.Context
	b.Subscribe(GameCreated, func(event Event) {
		gotCtx = event.Ctx
	})

	b.Publish(Event{Type: GameCreated, Ctx: ctx})

	require.Eventually(t, func() bool {
		return gotCtx != nil
	}, 2*time.Second, 50*time.Millisecond)

	assert.Equal(t, "value", gotCtx.Value(contextKey{}))
}

// MiddlewareBus tests

func TestMiddlewareBus_PublishSubscribe(t *testing.T) {
	mb := NewMiddlewareBus()
	defer mb.Stop()

	var called atomic.Int32
	mb.Subscribe(GameCreated, func(event Event) {
		called.Add(1)
	})

	mb.Publish(Event{Type: GameCreated})

	require.Eventually(t, func() bool {
		return called.Load() == 1
	}, 2*time.Second, 50*time.Millisecond)
}

func TestMiddlewareBus_Middleware(t *testing.T) {
	mb := NewMiddlewareBus()
	defer mb.Stop()

	var order []string
	var mu sync.Mutex
	mb.Use(func(next Handler) Handler {
		return func(event Event) {
			mu.Lock()
			order = append(order, "mw1_before")
			mu.Unlock()
			next(event)
			mu.Lock()
			order = append(order, "mw1_after")
			mu.Unlock()
		}
	})
	mb.Use(func(next Handler) Handler {
		return func(event Event) {
			mu.Lock()
			order = append(order, "mw2_before")
			mu.Unlock()
			next(event)
			mu.Lock()
			order = append(order, "mw2_after")
			mu.Unlock()
		}
	})

	mb.Subscribe(GameCreated, func(event Event) {
		mu.Lock()
		order = append(order, "handler")
		mu.Unlock()
	})

	mb.Publish(Event{Type: GameCreated})

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		expected := []string{"mw1_before", "mw2_before", "handler", "mw2_after", "mw1_after"}
		if len(order) != len(expected) {
			return false
		}
		for i := range expected {
			if order[i] != expected[i] {
				return false
			}
		}
		return true
	}, 2*time.Second, 50*time.Millisecond)
}
