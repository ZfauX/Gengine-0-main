// internal/pkg/websocket/room_hub_test.go
package websocket

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoomHub_RegisterClient(t *testing.T) {
	hub := NewRoomHub()
	hub.Run()
	t.Cleanup(hub.Stop)
	client := &Client{
		Send:   make(chan []byte, 10),
		RoomID: "room1",
		done:   make(chan struct{}),
	}

	hub.RegisterClient(client)

	assert.Eventually(t, func() bool {
		hub.mu.RLock()
		defer hub.mu.RUnlock()
		room, ok := hub.rooms["room1"]
		if !ok {
			return false
		}
		if len(room) != 1 {
			return false
		}
		for c := range room {
			return c == client
		}
		return false
	}, time.Second, 50*time.Millisecond)
}

func TestRoomHub_RegisterClient_Multiple(t *testing.T) {
	hub := NewRoomHub()
	hub.Run()
	t.Cleanup(hub.Stop)
	c1 := &Client{Send: make(chan []byte, 10), RoomID: "room1", done: make(chan struct{})}
	c2 := &Client{Send: make(chan []byte, 10), RoomID: "room1", done: make(chan struct{})}
	c3 := &Client{Send: make(chan []byte, 10), RoomID: "room2", done: make(chan struct{})}

	hub.RegisterClient(c1)
	hub.RegisterClient(c2)
	hub.RegisterClient(c3)

	assert.Eventually(t, func() bool {
		hub.mu.RLock()
		defer hub.mu.RUnlock()
		return len(hub.rooms["room1"]) == 2 && len(hub.rooms["room2"]) == 1
	}, 1*time.Second, 50*time.Millisecond)
}

func TestRoomHub_UnregisterClient(t *testing.T) {
	hub := NewRoomHub()
	hub.Run()
	t.Cleanup(hub.Stop)
	client := &Client{Send: make(chan []byte, 10), RoomID: "room1", done: make(chan struct{})}
	hub.RegisterClient(client)

	hub.UnregisterClient(client)

	// Ждём обработки unregister
	assert.Eventually(t, func() bool {
		hub.mu.RLock()
		_, ok := hub.rooms["room1"]
		hub.mu.RUnlock()
		return !ok
	}, 1*time.Second, 50*time.Millisecond)
}

func TestRoomHub_UnregisterClient_NotExists(t *testing.T) {
	hub := NewRoomHub()
	hub.Run()
	t.Cleanup(hub.Stop)
	client := &Client{Send: make(chan []byte, 10), RoomID: "room1", done: make(chan struct{})}
	hub.UnregisterClient(client)
}

func TestRoomHub_BroadcastToRoom(t *testing.T) {
	hub := NewRoomHub()
	hub.Run()
	t.Cleanup(hub.Stop)
	roomID := "testroom"

	c1 := &Client{Send: make(chan []byte, 10), RoomID: roomID, done: make(chan struct{})}
	c2 := &Client{Send: make(chan []byte, 10), RoomID: roomID, done: make(chan struct{})}
	hub.RegisterClient(c1)
	hub.RegisterClient(c2)

	msg := map[string]string{"event": "test", "data": "hello"}
	data, err := json.Marshal(msg)
	require.NoError(t, err)
	hub.BroadcastToRoom(roomID, data)

	// Ждём получения сообщений
	var wg sync.WaitGroup
	wg.Add(2)
	var received1, received2 map[string]string
	var fatalErr atomic.Value

	go func() {
		defer wg.Done()
		select {
		case received := <-c1.Send:
			err := json.Unmarshal(received, &received1)
			if err != nil {
				fatalErr.Store(err)
			}
		case <-time.After(2 * time.Second):
			fatalErr.Store(fmt.Errorf("client 1 did not receive message"))
		}
	}()

	go func() {
		defer wg.Done()
		select {
		case received := <-c2.Send:
			err := json.Unmarshal(received, &received2)
			if err != nil {
				fatalErr.Store(err)
			}
		case <-time.After(2 * time.Second):
			fatalErr.Store(fmt.Errorf("client 2 did not receive message"))
		}
	}()

	wg.Wait()

	if e := fatalErr.Load(); e != nil {
		require.NoError(t, e.(error))
	}
	assert.Equal(t, "hello", received1["data"])
	assert.Equal(t, "hello", received2["data"])
}

func TestRoomHub_BroadcastToRoom_NoClients(t *testing.T) {
	hub := NewRoomHub()
	hub.Run()
	t.Cleanup(hub.Stop)
	hub.BroadcastToRoom("nonexistent", []byte("test"))
}

func TestRoomHub_BroadcastToRoom_WithClosedClient(t *testing.T) {
	hub := NewRoomHub()
	hub.Run()
	t.Cleanup(hub.Stop)
	roomID := "testroom"

	c1 := &Client{
		Send:   make(chan []byte, 10),
		RoomID: roomID,
		done:   make(chan struct{}),
	}
	c2 := &Client{
		Send:   make(chan []byte, 10),
		RoomID: roomID,
		done:   make(chan struct{}),
	}
	hub.RegisterClient(c1)
	hub.RegisterClient(c2)

	c1.Close()

	msg := map[string]string{"event": "test"}
	data, err := json.Marshal(msg)
	require.NoError(t, err)
	hub.BroadcastToRoom(roomID, data)

	// Ждём обработки закрытия
	assert.Eventually(t, func() bool {
		hub.mu.RLock()
		defer hub.mu.RUnlock()
		_, exists := hub.rooms[roomID][c1]
		return !exists
	}, 1*time.Second, 50*time.Millisecond)

	hub.mu.RLock()
	room := hub.rooms[roomID]
	_, hasC1 := room[c1]
	_, hasC2 := room[c2]
	hub.mu.RUnlock()
	assert.False(t, hasC1, "closed client should be removed")
	assert.True(t, hasC2, "open client should remain")

	var received bool
	select {
	case <-c2.Send:
		received = true
	case <-time.After(100 * time.Millisecond):
	}
	assert.True(t, received, "client 2 should receive message")
}

func TestRoomHub_BroadcastToRoom_FullChannel(t *testing.T) {
	hub := NewRoomHub()
	hub.Run()
	t.Cleanup(hub.Stop)
	roomID := "testroom"

	c1 := &Client{
		Send:   make(chan []byte, 1),
		RoomID: roomID,
		done:   make(chan struct{}),
	}
	hub.RegisterClient(c1)

	c1.Send <- []byte("full")

	msg := map[string]string{"event": "test"}
	data, err := json.Marshal(msg)
	require.NoError(t, err)
	hub.BroadcastToRoom(roomID, data)

	// Ждём обработки (сообщение будет отброшено, клиент не отключится)
	assert.Eventually(t, func() bool {
		hub.mu.RLock()
		defer hub.mu.RUnlock()
		return len(hub.rooms[roomID]) == 1
	}, 500*time.Millisecond, 50*time.Millisecond)

	hub.mu.RLock()
	roomLen := len(hub.rooms[roomID])
	roomExists := hub.rooms[roomID] != nil
	hub.mu.RUnlock()
	assert.True(t, roomExists, "client should NOT be removed - message is dropped instead of disconnecting")
	assert.Equal(t, 1, roomLen, "client should remain connected after buffer full")
}

func TestClient_Close(t *testing.T) {
	c := &Client{
		Send: make(chan []byte, 10),
		done: make(chan struct{}),
	}
	assert.False(t, c.closed)
	c.Close()
	assert.True(t, c.closed)
	c.Close()
	assert.True(t, c.closed)
}

func TestClient_Close_ChannelClosedOnce(t *testing.T) {
	c := &Client{
		Send: make(chan []byte, 10),
		done: make(chan struct{}),
	}
	c.Close()
	// Send channel is NOT closed anymore (done is closed instead)
	assert.NotPanics(t, func() {
		select {
		case c.Send <- []byte("test"):
		default:
		}
	})
	// done channel IS closed
	_, ok := <-c.done
	assert.False(t, ok)
}

func TestRoomHub_Presence(t *testing.T) {
	// IDEA-6: RoomClientCount / RoomUserIDs / onRoomChange callback.
	hub := NewRoomHub()
	hub.Run()
	t.Cleanup(hub.Stop)

	var eventsMu sync.Mutex
	var events []string
	hub.SetOnRoomChange(func(roomID string) {
		eventsMu.Lock()
		events = append(events, roomID)
		eventsMu.Unlock()
	})

	roomID := "room1"
	c1 := &Client{Send: make(chan []byte, 10), RoomID: roomID, UserID: 7, done: make(chan struct{})}
	c2 := &Client{Send: make(chan []byte, 10), RoomID: roomID, UserID: 7, done: make(chan struct{})}
	c3 := &Client{Send: make(chan []byte, 10), RoomID: roomID, UserID: 9, done: make(chan struct{})}

	hub.RegisterClient(c1)
	hub.RegisterClient(c2)
	hub.RegisterClient(c3)

	// Ждём регистрации всех.
	assert.Eventually(t, func() bool {
		return hub.RoomClientCount(roomID) == 3
	}, time.Second, 50*time.Millisecond)
	// Уникальные userID: 7 (два соединения) + 9 → 2.
	ids := hub.RoomUserIDs(roomID)
	assert.Len(t, ids, 2)

	// Колбэк вызывался минимум 3 раза для этой комнаты.
	assert.Eventually(t, func() bool {
		eventsMu.Lock()
		defer eventsMu.Unlock()
		n := 0
		for _, e := range events {
			if e == roomID {
				n++
			}
		}
		return n >= 3
	}, time.Second, 50*time.Millisecond)

	hub.UnregisterClient(c1)
	assert.Eventually(t, func() bool {
		return hub.RoomClientCount(roomID) == 2
	}, time.Second, 50*time.Millisecond)
}

func BenchmarkRoomHub_BroadcastToRoom(b *testing.B) {
	hub := NewRoomHub()
	hub.Run()
	b.Cleanup(hub.Stop)
	roomID := "benchroom"
	clients := make([]*Client, 100)
	for i := 0; i < 100; i++ {
		c := &Client{Send: make(chan []byte, 10), RoomID: roomID, done: make(chan struct{})}
		clients[i] = c
		hub.RegisterClient(c)
	}

	// Ждём регистрации клиентов
	assert.Eventually(b, func() bool {
		hub.mu.RLock()
		defer hub.mu.RUnlock()
		room, ok := hub.rooms[roomID]
		return ok && len(room) == 100
	}, 2*time.Second, 50*time.Millisecond)

	msg := map[string]string{"event": "bench"}
	data, err := json.Marshal(msg)
	require.NoError(b, err)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hub.BroadcastToRoom(roomID, data)
	}
}
