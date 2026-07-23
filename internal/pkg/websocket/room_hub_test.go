// internal/pkg/websocket/room_hub_test.go
package websocket

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoomHub_RegisterClient(t *testing.T) {
	hub := NewRoomHub()
	go hub.Run()
	t.Cleanup(hub.Stop)
	client := &Client{
		Send:   make(chan []byte, 10),
		RoomID: "room1",
		done:   make(chan struct{}),
	}

	hub.RegisterClient(client)

	hub.mu.RLock()
	room, ok := hub.rooms["room1"]
	hub.mu.RUnlock()
	assert.True(t, ok)
	assert.Contains(t, room, client)
	assert.Len(t, room, 1)
}

func TestRoomHub_RegisterClient_Multiple(t *testing.T) {
	hub := NewRoomHub()
	go hub.Run()
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
	go hub.Run()
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
	go hub.Run()
	t.Cleanup(hub.Stop)
	client := &Client{Send: make(chan []byte, 10), RoomID: "room1", done: make(chan struct{})}
	hub.UnregisterClient(client)
}

func TestRoomHub_BroadcastToRoom(t *testing.T) {
	hub := NewRoomHub()
	go hub.Run()
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
	var fatalErr error

	go func() {
		defer wg.Done()
		select {
		case received := <-c1.Send:
			err := json.Unmarshal(received, &received1)
			if err != nil {
				fatalErr = err
			}
		case <-time.After(2 * time.Second):
			fatalErr = fmt.Errorf("client 1 did not receive message")
		}
	}()

	go func() {
		defer wg.Done()
		select {
		case received := <-c2.Send:
			err := json.Unmarshal(received, &received2)
			if err != nil {
				fatalErr = err
			}
		case <-time.After(2 * time.Second):
			fatalErr = fmt.Errorf("client 2 did not receive message")
		}
	}()

	wg.Wait()

	require.NoError(t, fatalErr)
	assert.Equal(t, "hello", received1["data"])
	assert.Equal(t, "hello", received2["data"])
}

func TestRoomHub_BroadcastToRoom_NoClients(t *testing.T) {
	hub := NewRoomHub()
	go hub.Run()
	t.Cleanup(hub.Stop)
	hub.BroadcastToRoom("nonexistent", []byte("test"))
}

func TestRoomHub_BroadcastToRoom_WithClosedClient(t *testing.T) {
	hub := NewRoomHub()
	go hub.Run()
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
		room := hub.rooms[roomID]
		hub.mu.RUnlock()
		_, exists := room[c1]
		return !exists
	}, 1*time.Second, 50*time.Millisecond)

	hub.mu.RLock()
	room := hub.rooms[roomID]
	hub.mu.RUnlock()
	assert.NotContains(t, room, c1, "closed client should be removed")
	assert.Contains(t, room, c2, "open client should remain")

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
	go hub.Run()
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
		room := hub.rooms[roomID]
		hub.mu.RUnlock()
		return len(room) == 1
	}, 500*time.Millisecond, 50*time.Millisecond)

	hub.mu.RLock()
	room := hub.rooms[roomID]
	hub.mu.RUnlock()
	assert.NotNil(t, room, "client should NOT be removed - message is dropped instead of disconnecting")
	assert.Len(t, room, 1, "client should remain connected after buffer full")
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

func BenchmarkRoomHub_BroadcastToRoom(b *testing.B) {
	hub := NewRoomHub()
	go hub.Run()
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
		room, ok := hub.rooms[roomID]
		hub.mu.RUnlock()
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
