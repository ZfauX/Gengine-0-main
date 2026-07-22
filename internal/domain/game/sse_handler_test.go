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

func TestSSEHandler_Broadcast(t *testing.T) {
	// Очистка перед тестом
	sseMgr.mu.Lock()
	sseMgr.sessions = make(map[uint][]*sseSession)
	sseMgr.gameMap = make(map[*sseSession]uint)
	sseMgr.mu.Unlock()

	// Создаем тестовый response writer
	w := httptest.NewRecorder()
	flusher := &mockFlusher{recorder: w}

	// Регистрируем сессию
	session := sseMgr.RegisterSession(1, w, flusher)
	require.NotNil(t, session)

	// Отправляем событие
	sseMgr.Broadcast(1, "test_event", map[string]any{"key": "value"})

	// Ждём обработки
	assert.Eventually(t, func() bool {
		body := w.Body.String()
		return len(body) > 0
	}, 1*time.Second, 50*time.Millisecond)

	// Проверяем ответ
	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "event: test_event")
	assert.Contains(t, w.Body.String(), `"key":"value"`)

	// Удаляем сессию
	sseMgr.UnregisterSession(session)
}

func TestSSEHandler_Broadcast_MultipleSessions(t *testing.T) {
	// Очистка перед тестом
	sseMgr.mu.Lock()
	sseMgr.sessions = make(map[uint][]*sseSession)
	sseMgr.gameMap = make(map[*sseSession]uint)
	sseMgr.mu.Unlock()

	w1 := httptest.NewRecorder()
	w2 := httptest.NewRecorder()
	flusher1 := &mockFlusher{recorder: w1}
	flusher2 := &mockFlusher{recorder: w2}

	session1 := sseMgr.RegisterSession(2, w1, flusher1)
	session2 := sseMgr.RegisterSession(2, w2, flusher2)

	sseMgr.Broadcast(2, "multi_event", nil)

	// Ждём обработки
	assert.Eventually(t, func() bool {
		return len(w1.Body.String()) > 0 && len(w2.Body.String()) > 0
	}, 1*time.Second, 50*time.Millisecond)

	assert.Contains(t, w1.Body.String(), "event: multi_event")
	assert.Contains(t, w2.Body.String(), "event: multi_event")

	sseMgr.UnregisterSession(session1)
	sseMgr.UnregisterSession(session2)
}

func TestToJSON(t *testing.T) {
	data := map[string]any{"test": 123}
	result := toJSON(data)
	assert.Contains(t, result, `"test":123`)

	// Проверка на nil — JSON marshal возвращает "null"
	assert.Equal(t, "null", toJSON(nil))
}

func TestSSEHandler_ConnectionClose(t *testing.T) {
	sseMgr.mu.Lock()
	sseMgr.sessions = make(map[uint][]*sseSession)
	sseMgr.gameMap = make(map[*sseSession]uint)
	sseMgr.mu.Unlock()

	w := httptest.NewRecorder()
	flusher := &mockFlusher{recorder: w}
	session := sseMgr.RegisterSession(3, w, flusher)

	// Отправляем событие перед закрытием
	sseMgr.Broadcast(3, "before_close", map[string]any{"status": "ok"})

	// Закрываем сессию
	sseMgr.UnregisterSession(session)

	// Сессия должна быть удалена
	sseMgr.mu.Lock()
	assert.Len(t, sseMgr.sessions[3], 0)
	assert.NotContains(t, sseMgr.gameMap, session)
	sseMgr.mu.Unlock()
}
