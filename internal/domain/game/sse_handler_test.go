// internal/domain/game/sse_handler_test.go
package game

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockFlusher — mock для http.Flusher, который вызывает Flush на recorder.
type mockFlusher struct {
	recorder *httptest.ResponseRecorder
}

func (m *mockFlusher) Flush() {
	m.recorder.Flush()
}

// safeRecorder — thread-safe обёртка над ResponseRecorder: writeLoop пишет
// в горутине, а тест читает body — без мьютекса data race (-race).
type safeRecorder struct {
	mu  sync.Mutex
	rec *httptest.ResponseRecorder
	fl  *mockFlusher
}

func newSafeRecorder() *safeRecorder {
	rec := httptest.NewRecorder()
	return &safeRecorder{
		rec: rec,
		fl:  &mockFlusher{recorder: rec},
	}
}

func (s *safeRecorder) Header() http.Header { return s.rec.Header() }

func (s *safeRecorder) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rec.Write(b)
}

func (s *safeRecorder) WriteHeader(statusCode int) { s.rec.WriteHeader(statusCode) }

func (s *safeRecorder) Flush() { s.fl.Flush() }

func (s *safeRecorder) body() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rec.Body.String()
}

func (s *safeRecorder) code() int { return s.rec.Code }

func newTestSSEMgr() *SSEManager {
	return &SSEManager{
		sessions:      make(map[uint][]*SSESession),
		gameMap:       make(map[*SSESession]uint),
		stopCh:        make(chan struct{}),
		maxTotalConns: 1000,
		maxConnsPerIP: 100,
		connsPerIP:    make(map[string]int),
	}
}

func TestSSEHandler_Broadcast(t *testing.T) {
	mgr := newTestSSEMgr()

	sr := newSafeRecorder()

	session := mgr.RegisterSession(1, "127.0.0.1", sr, sr.fl)
	require.NotNil(t, session)

	mgr.Broadcast(1, "test_event", map[string]any{"key": "value"})

	assert.Eventually(t, func() bool {
		body := sr.body()
		return len(body) > 0
	}, 1*time.Second, 50*time.Millisecond)

	assert.Equal(t, 200, sr.code())
	assert.Contains(t, sr.body(), "event: test_event")
	assert.Contains(t, sr.body(), `"key":"value"`)

	mgr.UnregisterSession(session)
}

func TestSSEHandler_Broadcast_MultipleSessions(t *testing.T) {
	mgr := newTestSSEMgr()

	sr1 := newSafeRecorder()
	sr2 := newSafeRecorder()

	mgr.RegisterSession(2, "127.0.0.1", sr1, sr1.fl)
	mgr.RegisterSession(2, "127.0.0.1", sr2, sr2.fl)

	mgr.Broadcast(2, "multi_event", nil)

	assert.Eventually(t, func() bool {
		return len(sr1.body()) > 0 && len(sr2.body()) > 0
	}, 1*time.Second, 50*time.Millisecond)

	assert.Contains(t, sr1.body(), "event: multi_event")
	assert.Contains(t, sr2.body(), "event: multi_event")
}

func TestToJSON(t *testing.T) {
	data := map[string]any{"test": 123}
	result := toJSON(data)
	assert.Contains(t, result, `"test":123`)

	assert.Equal(t, "null", toJSON(nil))
}

func TestSSEHandler_ConcurrentBroadcast(t *testing.T) {
	mgr := newTestSSEMgr()

	sr := newSafeRecorder()
	session := mgr.RegisterSession(4, "127.0.0.1", sr, sr.fl)
	defer mgr.UnregisterSession(session)

	for i := 0; i < 10; i++ {
		mgr.Broadcast(4, "concurrent_event", map[string]any{"i": i})
	}

	assert.Eventually(t, func() bool {
		body := sr.body()
		return strings.Count(body, "event: concurrent_event") >= 10
	}, 2*time.Second, 50*time.Millisecond)
}

func TestSSEHandler_ConnectionClose(t *testing.T) {
	mgr := newTestSSEMgr()

	sr := newSafeRecorder()
	session := mgr.RegisterSession(3, "127.0.0.1", sr, sr.fl)

	mgr.Broadcast(3, "before_close", map[string]any{"status": "ok"})

	mgr.UnregisterSession(session)

	mgr.mu.Lock()
	assert.Len(t, mgr.sessions[3], 0)
	assert.NotContains(t, mgr.gameMap, session)
	mgr.mu.Unlock()
}

func TestSSEHandler_Stop_ClosesSessions(t *testing.T) {
	mgr := newTestSSEMgr()

	sr := newSafeRecorder()
	session := mgr.RegisterSession(1, "127.0.0.1", sr, sr.fl)
	require.NotNil(t, session)

	// Stop should close session.done
	mgr.Stop()

	select {
	case <-session.done:
		// session closed — OK
	case <-time.After(500 * time.Millisecond):
		t.Fatal("session.done was not closed after Stop()")
	}

	// Sessions map should be cleared
	mgr.mu.RLock()
	assert.Len(t, mgr.sessions, 0)
	assert.Len(t, mgr.gameMap, 0)
	mgr.mu.RUnlock()
}

func TestSSEHandler_Stop_NoBroadcastAfterStop(t *testing.T) {
	mgr := newTestSSEMgr()

	sr := newSafeRecorder()
	_ = mgr.RegisterSession(1, "127.0.0.1", sr, sr.fl)

	mgr.Stop()

	// Broadcast after Stop must not panic or block (WaitGroup misuse)
	done := make(chan struct{})
	go func() {
		defer close(done)
		mgr.Broadcast(1, "after_stop", map[string]any{"ok": true})
	}()

	select {
	case <-done:
		// Broadcast returned without hanging — OK
	case <-time.After(1 * time.Second):
		t.Fatal("Broadcast after Stop() hung (WaitGroup misuse?)")
	}
}

func TestSSEHandler_CanAccept_Limits(t *testing.T) {
	mgr := newTestSSEMgr()

	// Per-IP limit
	assert.True(t, mgr.CanAccept("1.2.3.4"))
	_ = mgr.RegisterSession(1, "1.2.3.4", httptest.NewRecorder(), &mockFlusher{})
	_ = mgr.RegisterSession(1, "1.2.3.4", httptest.NewRecorder(), &mockFlusher{})
	// maxConnsPerIP = 100 in test mgr — so still accepted
	assert.True(t, mgr.CanAccept("1.2.3.4"))

	// Set low per-IP limit
	mgr.SetLimits(0, 1) // maxPerIP = 1
	_ = mgr.RegisterSession(2, "5.6.7.8", httptest.NewRecorder(), &mockFlusher{})
	_ = mgr.RegisterSession(2, "5.6.7.8", httptest.NewRecorder(), &mockFlusher{})
	assert.False(t, mgr.CanAccept("5.6.7.8"))
	assert.True(t, mgr.CanAccept("9.9.9.9"))

	// After Stop, CanAccept should still return false (stopped)
	mgr.Stop()
	assert.False(t, mgr.CanAccept("9.9.9.9"))
}
