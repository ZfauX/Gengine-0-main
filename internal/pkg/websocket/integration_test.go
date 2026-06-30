// internal/pkg/websocket/integration_test.go
package websocket

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setUpWebsocketServer(t *testing.T) (*httptest.Server, *RoomHub) {
	t.Helper()
	hub := NewRoomHub()
	go hub.Run()

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		roomID := r.URL.Query().Get("room")
		if roomID == "" {
			roomID = "default"
		}
		client := NewClient(conn, roomID, "127.0.0.1")
		hub.RegisterClient(client)
		defer hub.UnregisterClient(client)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go client.writePump(ctx)

		_ = client.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		client.Conn.SetPongHandler(func(string) error {
			_ = client.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			return nil
		})

		for {
			_, _, err := client.Conn.ReadMessage()
			if err != nil {
				break
			}
		}
	}))

	return server, hub
}

func TestWebSocket_Integration_EchoBroadcast(t *testing.T) {
	server, hub := setUpWebsocketServer(t)
	defer server.Close()

	url := "ws" + server.URL[4:] + "?room=testroom"
	conn1, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	defer func() { _ = conn1.Close() }()

	conn2, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	defer func() { _ = conn2.Close() }()

	time.Sleep(100 * time.Millisecond)

	msg := map[string]string{"event": "test", "data": "hello"}
	data, err := json.Marshal(msg)
	require.NoError(t, err)
	hub.BroadcastToRoom("testroom", data)

	var received1, received2 map[string]string

	err = conn1.SetReadDeadline(time.Now().Add(2 * time.Second))
	require.NoError(t, err)
	_, data1, err := conn1.ReadMessage()
	require.NoError(t, err)
	err = json.Unmarshal(data1, &received1)
	require.NoError(t, err)

	err = conn2.SetReadDeadline(time.Now().Add(2 * time.Second))
	require.NoError(t, err)
	_, data2, err := conn2.ReadMessage()
	require.NoError(t, err)
	err = json.Unmarshal(data2, &received2)
	require.NoError(t, err)

	assert.Equal(t, "hello", received1["data"])
	assert.Equal(t, "hello", received2["data"])
}

func TestWebSocket_Integration_BroadcastToDifferentRooms(t *testing.T) {
	server, hub := setUpWebsocketServer(t)
	defer server.Close()

	url1 := "ws" + server.URL[4:] + "?room=roomA"
	url2 := "ws" + server.URL[4:] + "?room=roomB"

	connA, _, err := websocket.DefaultDialer.Dial(url1, nil)
	require.NoError(t, err)
	defer func() { _ = connA.Close() }()

	connB, _, err := websocket.DefaultDialer.Dial(url2, nil)
	require.NoError(t, err)
	defer func() { _ = connB.Close() }()

	time.Sleep(100 * time.Millisecond)

	msg := map[string]string{"msg": "only A"}
	data, err := json.Marshal(msg)
	require.NoError(t, err)
	hub.BroadcastToRoom("roomA", data)

	err = connA.SetReadDeadline(time.Now().Add(2 * time.Second))
	require.NoError(t, err)
	_, dataA, err := connA.ReadMessage()
	require.NoError(t, err)
	var receivedA map[string]string
	err = json.Unmarshal(dataA, &receivedA)
	require.NoError(t, err)
	assert.Equal(t, "only A", receivedA["msg"])

	err = connB.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	require.NoError(t, err)
	_, _, err = connB.ReadMessage()
	assert.Error(t, err)
}

func TestWebSocket_Integration_ClientDisconnect(t *testing.T) {
	server, hub := setUpWebsocketServer(t)
	defer server.Close()

	url := "ws" + server.URL[4:] + "?room=testroom"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	time.Sleep(100 * time.Millisecond)

	hub.mu.Lock()
	room, ok := hub.rooms["testroom"]
	hub.mu.Unlock()
	assert.True(t, ok)
	assert.Len(t, room, 1)

	_ = conn.Close()
	time.Sleep(100 * time.Millisecond)

	hub.mu.Lock()
	_, ok = hub.rooms["testroom"]
	hub.mu.Unlock()
	assert.False(t, ok, "room should be removed when empty")
}

func TestWebSocket_Integration_MultipleMessages(t *testing.T) {
	server, hub := setUpWebsocketServer(t)
	defer server.Close()

	url := "ws" + server.URL[4:] + "?room=multi"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	time.Sleep(100 * time.Millisecond)

	messages := []string{"one", "two", "three"}
	for _, msg := range messages {
		data := map[string]string{"data": msg}
		b, err := json.Marshal(data)
		require.NoError(t, err)
		hub.BroadcastToRoom("multi", b)
	}

	for i, expected := range messages {
		err = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		require.NoError(t, err)
		_, data, err := conn.ReadMessage()
		require.NoError(t, err)
		var received map[string]string
		err = json.Unmarshal(data, &received)
		require.NoError(t, err)
		assert.Equal(t, expected, received["data"], "message %d", i)
	}
}

func TestWebSocket_Integration_InvalidRoom(t *testing.T) {
	server, _ := setUpWebsocketServer(t)
	defer server.Close()

	url := "ws" + server.URL[4:]
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	err = conn.WriteMessage(websocket.PingMessage, nil)
	require.NoError(t, err)

	err = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	require.NoError(t, err)
	_, _, err = conn.ReadMessage()
	if err != nil {
		assert.Contains(t, err.Error(), "timeout", "expected timeout error, got %v", err)
	}
}
