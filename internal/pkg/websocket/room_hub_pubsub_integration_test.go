//go:build integration

// internal/pkg/websocket/room_hub_pubsub_integration_test.go
//
// Интеграционный тест cross-instance WebSocket через РЕАЛЬНЫЙ Valkey
// (не miniredis). Требует: VALKEY_HOST / VALKEY_PORT в окружении
// (например: VALKEY_HOST=172.30.73.28 VALKEY_PORT=6380 go test -tags=integration).
package websocket

import (
	"os"
	"testing"
	"time"

	"gengine-0/internal/pkg/realtimebus"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestHubRealValkey(t *testing.T, instanceID string) (*RoomHub, *redis.Client) {
	t.Helper()
	host := os.Getenv("VALKEY_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("VALKEY_PORT")
	if port == "" {
		port = "6379"
	}
	client := redis.NewClient(&redis.Options{Addr: host + ":" + port})
	t.Cleanup(func() { _ = client.Close() })

	bus := realtimebus.NewValkeyBus(client)
	t.Cleanup(bus.Close)

	hub := NewRoomHub()
	hub.SetPubSub(bus, instanceID, []byte("test-secret"))
	hub.Run()
	t.Cleanup(hub.Stop)
	return hub, client
}

func TestRoomHub_PubSub_RealValkey(t *testing.T) {
	hub1, _ := newTestHubRealValkey(t, "real-1")
	hub2, _ := newTestHubRealValkey(t, "real-2")

	// Клиент на инстансе 2.
	c2 := NewClient(&websocket.Conn{}, "room", "127.0.0.1")
	c2.Send = make(chan []byte, 32)
	hub2.RegisterClient(c2)
	defer hub2.UnregisterClient(c2)
	require.Eventually(t, func() bool {
		return hub2.RoomClientCount("room") == 1
	}, 3*time.Second, 10*time.Millisecond)

	// Инстанс 1 публикует — клиент на инстансе 2 получает через реальный pub/sub.
	hub1.BroadcastToRoom("room", []byte("real-valkey-cross-instance"))

	select {
	case msg := <-c2.Send:
		assert.Equal(t, "real-valkey-cross-instance", string(msg))
	case <-time.After(5 * time.Second):
		t.Fatal("client on instance 2 did not receive message via real Valkey pub/sub")
	}
}
