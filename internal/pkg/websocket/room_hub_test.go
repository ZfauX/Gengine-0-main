// internal/pkg/websocket/room_hub_test.go
package websocket

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoomHub_RegisterClient(t *testing.T) {
	hub := NewRoomHub()
	client := &Client{
		Send:   make(chan []byte, 10),
		RoomID: "room1",
	}

	hub.RegisterClient("room1", client)

	hub.mu.RLock()
	clients, ok := hub.rooms["room1"]
	hub.mu.RUnlock()
	assert.True(t, ok)
	assert.Contains(t, clients, client)
	assert.Len(t, clients, 1)
}

func TestRoomHub_RegisterClient_Multiple(t *testing.T) {
	hub := NewRoomHub()
	c1 := &Client{Send: make(chan []byte, 10), RoomID: "room1"}
	c2 := &Client{Send: make(chan []byte, 10), RoomID: "room1"}
	c3 := &Client{Send: make(chan []byte, 10), RoomID: "room2"}

	hub.RegisterClient("room1", c1)
	hub.RegisterClient("room1", c2)
	hub.RegisterClient("room2", c3)

	hub.mu.RLock()
	defer hub.mu.RUnlock()
	assert.Len(t, hub.rooms["room1"], 2)
	assert.Len(t, hub.rooms["room2"], 1)
}

func TestRoomHub_UnregisterClient(t *testing.T) {
	hub := NewRoomHub()
	client := &Client{Send: make(chan []byte, 10), RoomID: "room1"}
	hub.RegisterClient("room1", client)

	hub.UnregisterClient(client)

	// После удаления клиента комната должна быть удалена, так как она пуста
	hub.mu.RLock()
	_, ok := hub.rooms["room1"]
	hub.mu.RUnlock()
	assert.False(t, ok, "room should be removed when empty")
}

func TestRoomHub_UnregisterClient_NotExists(t *testing.T) {
	hub := NewRoomHub()
	client := &Client{Send: make(chan []byte, 10), RoomID: "room1"}

	// Не должно паниковать
	hub.UnregisterClient(client)
}

func TestRoomHub_BroadcastToRoom(t *testing.T) {
	hub := NewRoomHub()
	roomID := "testroom"

	c1 := &Client{Send: make(chan []byte, 10), RoomID: roomID}
	c2 := &Client{Send: make(chan []byte, 10), RoomID: roomID}
	hub.RegisterClient(roomID, c1)
	hub.RegisterClient(roomID, c2)

	msg := map[string]string{"event": "test", "data": "hello"}
	hub.BroadcastToRoom(roomID, msg)

	select {
	case received := <-c1.Send:
		var parsed map[string]string
		err := json.Unmarshal(received, &parsed)
		require.NoError(t, err)
		assert.Equal(t, "hello", parsed["data"])
	case <-time.After(100 * time.Millisecond):
		t.Fatal("client 1 did not receive message")
	}

	select {
	case received := <-c2.Send:
		var parsed map[string]string
		err := json.Unmarshal(received, &parsed)
		require.NoError(t, err)
		assert.Equal(t, "hello", parsed["data"])
	case <-time.After(100 * time.Millisecond):
		t.Fatal("client 2 did not receive message")
	}
}

func TestRoomHub_BroadcastToRoom_NoClients(t *testing.T) {
	hub := NewRoomHub()
	hub.BroadcastToRoom("nonexistent", "test")
}

func TestRoomHub_BroadcastToRoom_WithClosedClient(t *testing.T) {
	hub := NewRoomHub()
	roomID := "testroom"

	c1 := &Client{Send: make(chan []byte, 10), RoomID: roomID}
	c2 := &Client{Send: make(chan []byte, 10), RoomID: roomID}
	hub.RegisterClient(roomID, c1)
	hub.RegisterClient(roomID, c2)

	c1.Close()

	msg := map[string]string{"event": "test"}
	hub.BroadcastToRoom(roomID, msg)

	// Проверяем, что канал c1 закрыт (клиент не должен получать сообщения)
	_, ok := <-c1.Send
	assert.False(t, ok, "closed client channel should be closed and empty")

	// c2 должен получить сообщение
	select {
	case <-c2.Send:
		// ok
	case <-time.After(100 * time.Millisecond):
		t.Fatal("client 2 should receive message")
	}
}

func TestRoomHub_BroadcastToRoom_FullChannel(t *testing.T) {
	hub := NewRoomHub()
	roomID := "testroom"

	c1 := &Client{Send: make(chan []byte, 1), RoomID: roomID}
	hub.RegisterClient(roomID, c1)

	c1.Send <- []byte("full")

	msg := map[string]string{"event": "test"}
	hub.BroadcastToRoom(roomID, msg)

	// Проверяем, что клиент закрыт (с защитой мьютекса)
	c1.mu.Lock()
	isClosed := c1.closed
	c1.mu.Unlock()
	assert.True(t, isClosed)
}

func TestRoomHub_BroadcastToRoom_MarshalError(t *testing.T) {
	hub := NewRoomHub()
	roomID := "testroom"
	c1 := &Client{Send: make(chan []byte, 10), RoomID: roomID}
	hub.RegisterClient(roomID, c1)

	msg := make(chan int) // несериализуемый тип
	hub.BroadcastToRoom(roomID, msg)

	select {
	case <-c1.Send:
		t.Fatal("should not receive message due to marshal error")
	default:
	}
}

// =============================================================================
// Тесты для Client (основные методы)
// =============================================================================

func TestClient_Close(t *testing.T) {
	c := &Client{
		Send: make(chan []byte, 10),
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
	}
	c.Close()
	assert.Panics(t, func() {
		c.Send <- []byte("test")
	})
}

// =============================================================================
// Бенчмарки
// =============================================================================

func BenchmarkRoomHub_BroadcastToRoom(b *testing.B) {
	hub := NewRoomHub()
	roomID := "benchroom"
	clients := make([]*Client, 100)
	for i := 0; i < 100; i++ {
		c := &Client{Send: make(chan []byte, 10), RoomID: roomID}
		clients[i] = c
		hub.RegisterClient(roomID, c)
	}

	msg := map[string]string{"event": "bench"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hub.BroadcastToRoom(roomID, msg)
	}
}
