// internal/pkg/websocket/integration_test.go
package websocket

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setUpWebsocketServer создаёт тестовый HTTP-сервер с WebSocket-обработчиком.
func setUpWebsocketServer(t *testing.T) (*httptest.Server, *RoomHub) {
	t.Helper()
	hub := NewRoomHub()
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
		client := &Client{
			Conn:   conn,
			Send:   make(chan []byte, 10),
			RoomID: roomID,
		}
		hub.RegisterClient(roomID, client)
		defer hub.UnregisterClient(client)

		go client.writePump()

		for {
			_, _, err := conn.ReadMessage()
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

	time.Sleep(50 * time.Millisecond)

	msg := map[string]string{"event": "test", "data": "hello"}
	hub.BroadcastToRoom("testroom", msg)

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

	time.Sleep(50 * time.Millisecond)

	hub.BroadcastToRoom("roomA", map[string]string{"msg": "only A"})

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

	time.Sleep(50 * time.Millisecond)

	hub.mu.RLock()
	clients, ok := hub.rooms["testroom"]
	hub.mu.RUnlock()
	assert.True(t, ok)
	assert.Len(t, clients, 1)

	_ = conn.Close()
	time.Sleep(100 * time.Millisecond)

	hub.mu.RLock()
	clients, ok = hub.rooms["testroom"]
	hub.mu.RUnlock()
	assert.True(t, ok)
	assert.Empty(t, clients)
}

func TestWebSocket_Integration_MultipleMessages(t *testing.T) {
	server, hub := setUpWebsocketServer(t)
	defer server.Close()

	url := "ws" + server.URL[4:] + "?room=multi"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	time.Sleep(50 * time.Millisecond)

	messages := []string{"one", "two", "three"}
	for _, msg := range messages {
		hub.BroadcastToRoom("multi", map[string]string{"data": msg})
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

	// Отправляем ping и ожидаем, что соединение не будет разорвано в течение таймаута
	err = conn.WriteMessage(websocket.PingMessage, nil)
	require.NoError(t, err)

	// Устанавливаем ReadDeadline, чтобы не висеть долго, и просто проверяем, что соединение живо
	err = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	require.NoError(t, err)
	_, _, err = conn.ReadMessage()
	// Ожидаем либо ошибку таймаута (i/o timeout), либо успешное чтение (если пришёл pong)
	if err != nil {
		assert.Contains(t, err.Error(), "timeout", "expected timeout error, got %v", err)
	}
}
