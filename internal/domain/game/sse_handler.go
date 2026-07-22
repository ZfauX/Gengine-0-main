// internal/domain/game/sse_handler.go
package game

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// SSESession хранит данные сессии SSE
type SSESession struct {
	w     http.ResponseWriter
	flush http.Flusher
	done  chan struct{}
}

// SSEManager управляет SSE-подключениями для каждой игры
type SSEManager struct {
	mu       sync.RWMutex
	sessions map[uint][]*SSESession
	gameMap  map[*SSESession]uint
	stopOnce sync.Once
	stopCh   chan struct{}
}

const sseHeartbeatInterval = 15 * time.Second

// NewSSEManager создаёт новый управляемый SSE-менеджер.
func NewSSEManager() *SSEManager {
	return &SSEManager{
		sessions: make(map[uint][]*SSESession),
		gameMap:  make(map[*SSESession]uint),
		stopCh:   make(chan struct{}),
	}
}

// Stop останавливает менеджер и закрывает все сессии.
func (m *SSEManager) Stop() {
	m.stopOnce.Do(func() {
		close(m.stopCh)
		m.mu.Lock()
		for _, sessions := range m.sessions {
			for _, s := range sessions {
				close(s.done)
			}
		}
		m.sessions = make(map[uint][]*SSESession)
		m.gameMap = make(map[*SSESession]uint)
		m.mu.Unlock()
	})
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
func (m *SSEManager) RegisterSession(gameID uint, w http.ResponseWriter, flush http.Flusher) *SSESession {
	m.mu.Lock()
	defer m.mu.Unlock()

	session := &SSESession{w: w, flush: flush, done: make(chan struct{})}
	m.sessions[gameID] = append(m.sessions[gameID], session)
	if m.gameMap == nil {
		m.gameMap = make(map[*SSESession]uint)
	}
	m.gameMap[session] = gameID
	return session
}

// UnregisterSession удаляет SSE-подключение
func (m *SSEManager) UnregisterSession(session *SSESession) {
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
func (m *SSEManager) Broadcast(gameID uint, eventType string, data any) {
	m.mu.Lock()
	sessions := make([]*SSESession, len(m.sessions[gameID]))
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
			if _, err := s.w.Write([]byte(event)); err != nil {
				log.Debug().Err(err).Msg("SSE: write error")
				return
			}
			s.flush.Flush()
		}
	}
}

// SSEHandler возвращает обработчик для SSE-эндпоинта
func SSEHandler(mgr *SSEManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		gameID, err := strconv.Atoi(c.Param("game_id"))
		if err != nil || gameID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "неверный game_id"})
			return
		}

		w := c.Writer
		flusher, ok := w.(http.Flusher)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		session := mgr.RegisterSession(uint(gameID), w, flusher)
		defer mgr.UnregisterSession(session)

		ticker := time.NewTicker(sseHeartbeatInterval)
		defer ticker.Stop()

		for {
			select {
			case <-c.Request.Context().Done():
				return
			case <-ticker.C:
				if _, err := w.Write([]byte(": heartbeat\n\n")); err != nil {
					log.Debug().Err(err).Msg("SSE: heartbeat write error")
					return
				}
				flusher.Flush()
			}
		}
	}
}
