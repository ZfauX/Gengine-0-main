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
	go hub.Run()
	client := &Client{
		Send:   make(chan []byte, 10),
		RoomID: "room1",
	}

	hub.RegisterClient(client)
	time.Sleep(10 * time.Millisecond)

	hub.mu.Lock()
	room, ok := hub.rooms["room1"]
	hub.mu.Unlock()
	assert.True(t, ok)
	assert.Contains(t, room, client)
	assert.Len(t, room, 1)
}

func TestRoomHub_RegisterClient_Multiple(t *testing.T) {
	hub := NewRoomHub()
	go hub.Run()
	c1 := &Client{Send: make(chan []byte, 10), RoomID: "room1"}
	c2 := &Client{Send: make(chan []byte, 10), RoomID: "room1"}
	c3 := &Client{Send: make(chan []byte, 10), RoomID: "room2"}

	hub.RegisterClient(c1)
	hub.RegisterClient(c2)
	hub.RegisterClient(c3)
	time.Sleep(10 * time.Millisecond)

	hub.mu.Lock()
	defer hub.mu.Unlock()
	assert.Len(t, hub.rooms["room1"], 2)
	assert.Len(t, hub.rooms["room2"], 1)
}

func TestRoomHub_UnregisterClient(t *testing.T) {
	hub := NewRoomHub()
	go hub.Run()
	client := &Client{Send: make(chan []byte, 10), RoomID: "room1"}
	hub.RegisterClient(client)
	time.Sleep(10 * time.Millisecond)

	hub.UnregisterClient(client)
	time.Sleep(10 * time.Millisecond)

	hub.mu.Lock()
	_, ok := hub.rooms["room1"]
	hub.mu.Unlock()
	assert.False(t, ok, "room should be removed when empty")
}

func TestRoomHub_UnregisterClient_NotExists(t *testing.T) {
	hub := NewRoomHub()
	go hub.Run()
	client := &Client{Send: make(chan []byte, 10), RoomID: "room1"}
	hub.UnregisterClient(client)
}

func TestRoomHub_BroadcastToRoom(t *testing.T) {
	hub := NewRoomHub()
	go hub.Run()
	roomID := "testroom"

	c1 := &Client{Send: make(chan []byte, 10), RoomID: roomID}
	c2 := &Client{Send: make(chan []byte, 10), RoomID: roomID}
	hub.RegisterClient(c1)
	hub.RegisterClient(c2)
	time.Sleep(10 * time.Millisecond)

	msg := map[string]string{"event": "test", "data": "hello"}
	data, err := json.Marshal(msg)
	require.NoError(t, err)
	hub.BroadcastToRoom(roomID, data)

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
	go hub.Run()
	hub.BroadcastToRoom("nonexistent", []byte("test"))
}

func TestRoomHub_BroadcastToRoom_WithClosedClient(t *testing.T) {
	hub := NewRoomHub()
	go hub.Run()
	roomID := "testroom"

	c1 := &Client{Send: make(chan []byte, 10), RoomID: roomID}
	c2 := &Client{Send: make(chan []byte, 10), RoomID: roomID}
	hub.RegisterClient(c1)
	hub.RegisterClient(c2)
	time.Sleep(10 * time.Millisecond)

	c1.Close()

	msg := map[string]string{"event": "test"}
	data, err := json.Marshal(msg)
	require.NoError(t, err)
	hub.BroadcastToRoom(roomID, data)

	// Даём время на обработку broadcast
	time.Sleep(20 * time.Millisecond)

	hub.mu.Lock()
	room := hub.rooms[roomID]
	hub.mu.Unlock()
	assert.NotContains(t, room, c1, "closed client should be removed")
	assert.Contains(t, room, c2, "open client should remain")

	select {
	case <-c2.Send:
		// ok
	case <-time.After(100 * time.Millisecond):
		t.Fatal("client 2 should receive message")
	}
}

func TestRoomHub_BroadcastToRoom_FullChannel(t *testing.T) {
	hub := NewRoomHub()
	go hub.Run()
	roomID := "testroom"

	c1 := &Client{Send: make(chan []byte, 1), RoomID: roomID}
	hub.RegisterClient(c1)
	time.Sleep(10 * time.Millisecond)

	c1.Send <- []byte("full")

	msg := map[string]string{"event": "test"}
	data, err := json.Marshal(msg)
	require.NoError(t, err)
	hub.BroadcastToRoom(roomID, data)

	// Даём время на обработку broadcast
	time.Sleep(20 * time.Millisecond)

	hub.mu.Lock()
	room := hub.rooms[roomID]
	hub.mu.Unlock()
	assert.Nil(t, room, "client should be removed because channel is full")
}

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

func BenchmarkRoomHub_BroadcastToRoom(b *testing.B) {
	hub := NewRoomHub()
	go hub.Run()
	roomID := "benchroom"
	clients := make([]*Client, 100)
	for i := 0; i < 100; i++ {
		c := &Client{Send: make(chan []byte, 10), RoomID: roomID}
		clients[i] = c
		hub.RegisterClient(c)
	}
	time.Sleep(10 * time.Millisecond)

	msg := map[string]string{"event": "bench"}
	data, err := json.Marshal(msg)
	require.NoError(b, err)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hub.BroadcastToRoom(roomID, data)
	}
}
