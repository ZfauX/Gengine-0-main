package events

import (
	"context"
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

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(2), called.Load())
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

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(1), gameCalls.Load())  // только GameCreated
	assert.Equal(t, int32(2), allCalls.Load())    // все события
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
	time.Sleep(100 * time.Millisecond)

	// Второй обработчик должен выполниться несмотря на panic в первом
	assert.Equal(t, int32(1), called.Load())
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
	time.Sleep(50 * time.Millisecond)
	assert.True(t, started.Load() > 0)

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
	time.Sleep(50 * time.Millisecond)
}

func TestBus_EventWithContext(t *testing.T) {
	b := NewBusWithWorkers(1)
	defer b.Stop()

	ctx := context.WithValue(context.Background(), "key", "value")
	var gotCtx context.Context
	b.Subscribe(GameCreated, func(event Event) {
		gotCtx = event.Ctx
	})

	b.Publish(Event{Type: GameCreated, Ctx: ctx})
	time.Sleep(50 * time.Millisecond)

	require.NotNil(t, gotCtx)
	assert.Equal(t, "value", gotCtx.Value("key"))
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
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(1), called.Load())
}

func TestMiddlewareBus_Middleware(t *testing.T) {
	mb := NewMiddlewareBus()
	defer mb.Stop()

	var order []string
	mb.Use(func(next Handler) Handler {
		return func(event Event) {
			order = append(order, "mw1_before")
			next(event)
			order = append(order, "mw1_after")
		}
	})
	mb.Use(func(next Handler) Handler {
		return func(event Event) {
			order = append(order, "mw2_before")
			next(event)
			order = append(order, "mw2_after")
		}
	})

	mb.Subscribe(GameCreated, func(event Event) {
		order = append(order, "handler")
	})

	mb.Publish(Event{Type: GameCreated})
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, []string{"mw1_before", "mw2_before", "handler", "mw2_after", "mw1_after"}, order)
}
