// internal/domain/game/sse_handler_test.go
package game

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockFlusher — mock для http.Flusher, который просто вызывает recorder
type mockFlusher struct {
	recorder *httptest.ResponseRecorder
}

func (m *mockFlusher) Flush() {
	m.recorder.Flush()
}

func newTestSSEMgr() *SSEManager {
	return &SSEManager{
		sessions: make(map[uint][]*SSESession),
		gameMap:  make(map[*SSESession]uint),
		stopCh:   make(chan struct{}),
	}
}

func TestSSEHandler_Broadcast(t *testing.T) {
	mgr := newTestSSEMgr()

	w := httptest.NewRecorder()
	flusher := &mockFlusher{recorder: w}

	session := mgr.RegisterSession(1, w, flusher)
	require.NotNil(t, session)

	mgr.Broadcast(1, "test_event", map[string]any{"key": "value"})

	assert.Eventually(t, func() bool {
		body := w.Body.String()
		return len(body) > 0
	}, 1*time.Second, 50*time.Millisecond)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "event: test_event")
	assert.Contains(t, w.Body.String(), `"key":"value"`)

	mgr.UnregisterSession(session)
}

func TestSSEHandler_Broadcast_MultipleSessions(t *testing.T) {
	mgr := newTestSSEMgr()

	w1 := httptest.NewRecorder()
	w2 := httptest.NewRecorder()
	flusher1 := &mockFlusher{recorder: w1}
	flusher2 := &mockFlusher{recorder: w2}

	mgr.RegisterSession(2, w1, flusher1)
	mgr.RegisterSession(2, w2, flusher2)

	mgr.Broadcast(2, "multi_event", nil)

	assert.Eventually(t, func() bool {
		return len(w1.Body.String()) > 0 && len(w2.Body.String()) > 0
	}, 1*time.Second, 50*time.Millisecond)

	assert.Contains(t, w1.Body.String(), "event: multi_event")
	assert.Contains(t, w2.Body.String(), "event: multi_event")
}

func TestToJSON(t *testing.T) {
	data := map[string]any{"test": 123}
	result := toJSON(data)
	assert.Contains(t, result, `"test":123`)

	assert.Equal(t, "null", toJSON(nil))
}

func TestSSEHandler_ConnectionClose(t *testing.T) {
	mgr := newTestSSEMgr()

	w := httptest.NewRecorder()
	flusher := &mockFlusher{recorder: w}
	session := mgr.RegisterSession(3, w, flusher)

	mgr.Broadcast(3, "before_close", map[string]any{"status": "ok"})

	mgr.UnregisterSession(session)

	mgr.mu.Lock()
	assert.Len(t, mgr.sessions[3], 0)
	assert.NotContains(t, mgr.gameMap, session)
	mgr.mu.Unlock()
}
