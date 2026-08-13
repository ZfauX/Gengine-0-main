//go:build integration

// internal/domain/game/sse_pubsub_integration_test.go
//
// Интеграционный тест cross-instance SSE через РЕАЛЬНЫЙ Valkey (не miniredis).
// Запуск: VALKEY_HOST=... VALKEY_PORT=... go test -tags=integration ./internal/domain/game/
package game

import (
	"os"
	"testing"
	"time"

	"gengine-0/internal/pkg/realtimebus"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestSSEManager_PubSub_RealValkey(t *testing.T) {
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

	mgr1 := newTestSSEMgr()
	mgr1.SetPubSub(bus, "sse-real-1")
	t.Cleanup(mgr1.Stop)

	bus2 := realtimebus.NewValkeyBus(client)
	t.Cleanup(bus2.Close)
	mgr2 := newTestSSEMgr()
	mgr2.SetPubSub(bus2, "sse-real-2")
	t.Cleanup(mgr2.Stop)

	// Сессия на инстансе 2 для игры 7.
	sr2 := newSafeRecorder()
	sess2 := mgr2.RegisterSession(7, "127.0.0.1", sr2, sr2.fl)
	require.NotNil(t, sess2)

	// Инстанс 1 публикует (ретраи против асинхронной подписки).
	for range 5 {
		mgr1.Broadcast(7, "level_completed", map[string]any{"score": 100})
		time.Sleep(100 * time.Millisecond)
	}

	waitForSSEBody(t, sr2, "level_completed")
	require.Contains(t, sr2.body(), `"score":100`)
}
