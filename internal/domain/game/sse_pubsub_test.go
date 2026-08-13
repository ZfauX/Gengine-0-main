// internal/domain/game/sse_pubsub_test.go
//
// Тесты cross-instance рассылки SSEManager через Valkey pub/sub (miniredis):
//   - событие, опубликованное на инстансе 1, доходит до сессии на инстансе 2;
//   - отсутствие эха;
//   - без Valkey — только локальная рассылка (старое поведение).
package game

import (
	"strings"
	"testing"
	"time"

	"gengine-0/internal/pkg/realtimebus"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newTestSSEMgrWithBus(t *testing.T, mr *miniredis.Miniredis, instanceID string) *SSEManager {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	bus := realtimebus.NewValkeyBus(client)
	t.Cleanup(bus.Close)

	mgr := newTestSSEMgr()
	mgr.SetPubSub(bus, instanceID)
	t.Cleanup(mgr.Stop)
	return mgr
}

// waitForSSEBody ждёт появления подстроки в теле SSE-сессии.
func waitForSSEBody(t *testing.T, sr *safeRecorder, substr string) {
	t.Helper()
	require.Eventually(t, func() bool {
		return strings.Contains(sr.body(), substr)
	}, 5*time.Second, 20*time.Millisecond)
}

func TestSSEManager_PubSub_CrossInstance(t *testing.T) {
	mr := miniredis.RunT(t)

	mgr1 := newTestSSEMgrWithBus(t, mr, "sse-instance-1")
	mgr2 := newTestSSEMgrWithBus(t, mr, "sse-instance-2")

	// Сессия на инстансе 2 для игры 7.
	sr2 := newSafeRecorder()
	sess2 := mgr2.RegisterSession(7, "127.0.0.1", sr2, sr2.fl)
	require.NotNil(t, sess2)

	// Инстанс 1 публикует (ретраи против асинхронной подписки) — сессия на
	// инстансе 2 должна получить событие.
	for range 5 {
		mgr1.Broadcast(7, "level_completed", map[string]any{"score": 100})
		time.Sleep(100 * time.Millisecond)
	}

	waitForSSEBody(t, sr2, "level_completed")
	require.Contains(t, sr2.body(), `"score":100`)
}

func TestSSEManager_PubSub_NoEcho(t *testing.T) {
	mr := miniredis.RunT(t)

	mgr1 := newTestSSEMgrWithBus(t, mr, "sse-instance-1")
	mgr2 := newTestSSEMgrWithBus(t, mr, "sse-instance-2")

	sr1 := newSafeRecorder()
	sess1 := mgr1.RegisterSession(7, "127.0.0.1", sr1, sr1.fl)
	require.NotNil(t, sess1)

	sr2 := newSafeRecorder()
	sess2 := mgr2.RegisterSession(7, "127.0.0.1", sr2, sr2.fl)
	require.NotNil(t, sess2)

	// Публикуем N раз. Ожидаем РОВНО n событий у каждой сессии (без эха).
	const n = 3
	for range n {
		mgr1.Broadcast(7, "hint_available", map[string]any{"hint": "x"})
		time.Sleep(120 * time.Millisecond)
	}

	waitForSSEBody(t, sr1, "hint_available")
	waitForSSEBody(t, sr2, "hint_available")

	// Дублей не должно быть (echo от подписки на собственном инстансе).
	// Считаем вхождение event-строки (не JSON-payload, где type повторяется).
	time.Sleep(500 * time.Millisecond)
	require.Contains(t, sr1.body(), "hint_available")
	require.Equal(t, n, countOccurrences(sr1.body(), "event: hint_available"),
		"local session must receive event exactly n times (no echo)")
	require.Equal(t, n, countOccurrences(sr2.body(), "event: hint_available"),
		"remote session must receive event exactly n times")
}

func TestSSEManager_PubSub_WithoutBus(t *testing.T) {
	mgr := newTestSSEMgr()
	sr := newSafeRecorder()
	sess := mgr.RegisterSession(7, "127.0.0.1", sr, sr.fl)
	require.NotNil(t, sess)

	mgr.Broadcast(7, "time_warning", map[string]any{"left": 60})
	waitForSSEBody(t, sr, "time_warning")
}

func countOccurrences(s, substr string) int {
	return strings.Count(s, substr)
}
