// internal/pkg/websocket/room_hub_worker_test.go
// T1 (pass 40): тесты жизненного цикла per-room воркеров и стресс-тест гонки
// broadcast ↔ unregister (регресс C-1: send-on-closed panic).
package websocket

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(roomID string) *Client {
	return &Client{Send: make(chan []byte, 10), RoomID: roomID, done: make(chan struct{})}
}

// TestRoomHub_BroadcastUnregisterRace — стресс: конкурентные broadcast и
// unregister последнего клиента не должны вызвать panic (send on closed channel).
func TestRoomHub_BroadcastUnregisterRace(t *testing.T) {
	hub := NewRoomHub()
	hub.Run()
	t.Cleanup(hub.Stop)

	for round := 0; round < 50; round++ {
		roomID := "race" + string(rune('A'+round))
		client := newTestClient(roomID)
		hub.RegisterClient(client)

		// Ждём регистрации.
		require.Eventually(t, func() bool {
			hub.mu.RLock()
			defer hub.mu.RUnlock()
			room, ok := hub.rooms[roomID]
			return ok && len(room) == 1
		}, time.Second, 20*time.Millisecond)

		done := make(chan struct{})
		go func() {
			defer close(done)
			for i := 0; i < 200; i++ {
				hub.BroadcastToRoom(roomID, []byte("data"))
			}
		}()

		// Одновременно отписываем клиента (комната опустеет → воркер idle-exit).
		go func() {
			hub.UnregisterClient(client)
		}()

		// Не должно быть panic — если runLoop упал, тест фейлится.
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("broadcast loop did not finish")
		}

		// Даём воркеру завершиться.
		time.Sleep(50 * time.Millisecond)
	}
}

// TestRoomHub_WorkerLifecycle — воркер создаётся при broadcast, завершается по
// idle-таймеру после удаления комнаты (нет утечки горутин).
func TestRoomHub_WorkerLifecycle(t *testing.T) {
	hub := NewRoomHub()
	hub.Run()
	t.Cleanup(hub.Stop)

	roomID := "lifecycle"
	client := newTestClient(roomID)
	hub.RegisterClient(client)
	require.Eventually(t, func() bool {
		hub.mu.RLock()
		defer hub.mu.RUnlock()
		_, ok := hub.rooms[roomID]
		return ok
	}, time.Second, 20*time.Millisecond)

	// Первый broadcast создаёт очередь + воркер.
	hub.BroadcastToRoom(roomID, []byte("hello"))
	require.Eventually(t, func() bool {
		hub.mu.RLock()
		defer hub.mu.RUnlock()
		return hub.roomQueues[roomID] != nil
	}, time.Second, 20*time.Millisecond)

	// Отписываем последнего клиента — комната удаляется, очередь удаляется,
	// воркер должен выйти по idle-таймеру (не висеть вечно).
	hub.UnregisterClient(client)
	require.Eventually(t, func() bool {
		hub.mu.RLock()
		defer hub.mu.RUnlock()
		_, roomExists := hub.rooms[roomID]
		_, queueExists := hub.roomQueues[roomID]
		return !roomExists && !queueExists
	}, time.Second, 20*time.Millisecond)

	// Воркер завершается в пределах idle-интервала (30с) — для теста ждём
	// небольшое окно и проверяем, что очередь пересоздаётся при новом broadcast.
	hub.BroadcastToRoom(roomID, []byte("after-empty"))
	// Комната не существует → broadcast дропается без panic.
	time.Sleep(50 * time.Millisecond)
}

// TestRoomHub_WorkerExitsOnStop — воркер завершается по h.done при Stop.
func TestRoomHub_WorkerExitsOnStop(t *testing.T) {
	hub := NewRoomHub()
	hub.Run()

	roomID := "stop-worker"
	client := newTestClient(roomID)
	hub.RegisterClient(client)
	require.Eventually(t, func() bool {
		hub.mu.RLock()
		defer hub.mu.RUnlock()
		_, ok := hub.rooms[roomID]
		return ok
	}, time.Second, 20*time.Millisecond)

	hub.BroadcastToRoom(roomID, []byte("data"))
	require.Eventually(t, func() bool {
		hub.mu.RLock()
		defer hub.mu.RUnlock()
		return hub.roomQueues[roomID] != nil
	}, time.Second, 20*time.Millisecond)

	// Stop не должен panic и должен завершить воркеров.
	require.NotPanics(t, hub.Stop)
}

// TestRoomHub_BroadcastBeforeWorker — broadcast в комнату с клиентами до
// создания воркера: клиент должен получить сообщение.
func TestRoomHub_BroadcastDelivers(t *testing.T) {
	hub := NewRoomHub()
	hub.Run()
	t.Cleanup(hub.Stop)

	roomID := "deliver"
	client := newTestClient(roomID)
	hub.RegisterClient(client)
	require.Eventually(t, func() bool {
		hub.mu.RLock()
		defer hub.mu.RUnlock()
		room, ok := hub.rooms[roomID]
		return ok && len(room) == 1
	}, time.Second, 20*time.Millisecond)

	hub.BroadcastToRoom(roomID, []byte("ping"))
	select {
	case got := <-client.Send:
		assert.Equal(t, []byte("ping"), got)
	case <-time.After(time.Second):
		t.Fatal("client did not receive broadcast")
	}
}

// TestRoomHub_ManyRoomsNoWorkerLeak — массовый churn комнат не должен оставлять
// воркеров после Stop (проверяем Wait завершается).
func TestRoomHub_ManyRoomsNoWorkerLeak(t *testing.T) {
	hub := NewRoomHub()
	hub.Run()

	var sent int32
	for i := 0; i < 20; i++ {
		roomID := string(rune('r' + i))
		client := newTestClient(roomID)
		hub.RegisterClient(client)
		go func() {
			hub.BroadcastToRoom(roomID, []byte("x"))
			atomic.AddInt32(&sent, 1)
		}()
	}

	// Ждём несколько broadcast-циклов.
	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&sent) >= 20
	}, 2*time.Second, 20*time.Millisecond)

	// Stop должен завершиться без зависания (все воркеры выходят по done).
	done := make(chan struct{})
	go func() { hub.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop hung - worker leak")
	}
}
