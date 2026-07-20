// internal/domain/game/sse_handler.go
package game

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// sseSession хранит данные сессии SSE
type sseSession struct {
	w     http.ResponseWriter
	flush http.Flusher
	done  chan struct{}
}

// sseManager управляет SSE-подключениями для каждой игры
type sseManager struct {
	mu       sync.Mutex
	sessions map[uint][]*sseSession
	gameMap  map[*sseSession]uint // session -> gameID mapping
}

var sseMgr = &sseManager{
	sessions: make(map[uint][]*sseSession),
}

// toJSON сериализует значение в JSON-строку
func toJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// RegisterSession добавляет новое SSE-подключение для игры
func (m *sseManager) RegisterSession(gameID uint, w http.ResponseWriter, flush http.Flusher) *sseSession {
	m.mu.Lock()
	defer m.mu.Unlock()

	session := &sseSession{w: w, flush: flush, done: make(chan struct{})}
	m.sessions[gameID] = append(m.sessions[gameID], session)
	if m.gameMap == nil {
		m.gameMap = make(map[*sseSession]uint)
	}
	m.gameMap[session] = gameID
	return session
}

// UnregisterSession удаляет SSE-подключение
func (m *sseManager) UnregisterSession(session *sseSession) {
	m.mu.Lock()
	defer m.mu.Unlock()

	gameID, ok := m.gameMap[session]
	if !ok {
		return
	}
	delete(m.gameMap, session)

	sessions := m.sessions[gameID]
	for i, s := range sessions {
		if s == session {
			m.sessions[gameID] = append(sessions[:i], sessions[i+1:]...)
			close(session.done)
			break
		}
	}
	if len(m.sessions[gameID]) == 0 {
		delete(m.sessions, gameID)
	}
}

// Broadcast отправляет событие всем подписчикам игры
func (m *sseManager) Broadcast(gameID uint, eventType string, data any) {
	m.mu.Lock()
	sessions := make([]*sseSession, len(m.sessions[gameID]))
	copy(sessions, m.sessions[gameID])
	m.mu.Unlock()

	payload := map[string]any{
		"type":    eventType,
		"game_id": gameID,
		"data":    data,
		"time":    time.Now().Format(time.RFC3339),
	}

	for _, s := range sessions {
		select {
		case <-s.done:
			continue
		default:
			event := "event: " + eventType + "\ndata: " + toJSON(payload) + "\n\n"
			s.w.Write([]byte(event))
			s.flush.Flush()
		}
	}
}

// SSEHandler возвращает обработчик для SSE-эндпоинта
func SSEHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		gameID, err := strconv.Atoi(c.Param("game_id"))
		if err != nil || gameID <= 0 {
			c.JSON(400, gin.H{"error": "неверный game_id"})
			return
		}

		w := c.Writer
		flusher, ok := w.(http.Flusher)
		if !ok {
			c.JSON(500, gin.H{"error": "streaming not supported"})
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		session := sseMgr.RegisterSession(uint(gameID), w, flusher)
		defer sseMgr.UnregisterSession(session)

		// Отправляем heartbeat каждые 15 секунд
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-c.Request.Context().Done():
				return
			case <-ticker.C:
				w.Write([]byte(": heartbeat\n\n"))
				flusher.Flush()
			}
		}
	}
}

// SSEBroadcaster вызывается из сервисов для отправки событий подписчикам
func SSEBroadcaster(gameID uint, eventType string, data any) {
	sseMgr.Broadcast(gameID, eventType, data)
}
