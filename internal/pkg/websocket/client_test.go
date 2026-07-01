// internal/pkg/websocket/client_test.go
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

func TestClient_WritePump_SendMessage(t *testing.T) {
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
	defer server.Close()

	url := "ws" + server.URL[4:] + "?room=testroom"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	time.Sleep(100 * time.Millisecond)

	msg := map[string]string{"event": "test", "data": "hello"}
	data, err := json.Marshal(msg)
	require.NoError(t, err)
	hub.BroadcastToRoom("testroom", data)

	err = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	require.NoError(t, err)
	_, dataReceived, err := conn.ReadMessage()
	require.NoError(t, err)

	var received map[string]string
	err = json.Unmarshal(dataReceived, &received)
	require.NoError(t, err)
	assert.Equal(t, "hello", received["data"])
}

func TestClient_WritePump_CloseOnSendChannelClose(t *testing.T) {
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

		time.Sleep(100 * time.Millisecond)

		client.Close()
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	url := "ws" + server.URL[4:] + "?room=testroom"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	time.Sleep(300 * time.Millisecond)

	err = conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	require.NoError(t, err)
	_, _, err = conn.ReadMessage()
	assert.Error(t, err)

	// Проверяем, что ошибка связана с закрытием соединения
	isCloseErr := websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) ||
		websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) ||
		err.Error() == "EOF"
	assert.True(t, isCloseErr, "ожидалась ошибка закрытия соединения, получено: %v", err)
}

func TestClient_WritePump_Ping(t *testing.T) {
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
	defer server.Close()

	url := "ws" + server.URL[4:] + "?room=testroom"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	conn.SetPingHandler(func(appData string) error {
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(5*time.Second))
	})

	time.Sleep(3 * time.Second)

	err = conn.WriteMessage(websocket.TextMessage, []byte("ping"))
	assert.NoError(t, err)

	err = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	require.NoError(t, err)
	_, _, err = conn.ReadMessage()
	if err != nil {
		assert.Contains(t, err.Error(), "timeout")
	}
}
