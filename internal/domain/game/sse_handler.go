// internal/domain/game/sse_handler.go
package game

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// SSESession хранит данные сессии SSE
type SSESession struct {
	mu        sync.Mutex
	w         http.ResponseWriter
	flush     http.Flusher
	done      chan struct{}
	closeOnce sync.Once
	remoteIP  string
}

// SSEManager управляет SSE-подключениями для каждой игры
type SSEManager struct {
	mu            sync.RWMutex
	sessions      map[uint][]*SSESession
	gameMap       map[*SSESession]uint
	stopOnce      sync.Once
	stopCh        chan struct{}
	maxTotalConns int
	maxConnsPerIP int
	totalConns    int
	connsPerIP    map[string]int
}

const (
	sseHeartbeatInterval = 15 * time.Second
	defaultSSEMaxTotal   = 500
	defaultSSEMaxPerIP   = 50
)

// NewSSEManager создаёт новый управляемый SSE-менеджер.
func NewSSEManager() *SSEManager {
	return &SSEManager{
		sessions:      make(map[uint][]*SSESession),
		gameMap:       make(map[*SSESession]uint),
		stopCh:        make(chan struct{}),
		maxTotalConns: defaultSSEMaxTotal,
		maxConnsPerIP: defaultSSEMaxPerIP,
		connsPerIP:    make(map[string]int),
	}
}

// SetLimits устанавливает лимиты SSE-соединений.
func (m *SSEManager) SetLimits(maxTotal, maxPerIP int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if maxTotal > 0 {
		m.maxTotalConns = maxTotal
	}
	if maxPerIP > 0 {
		m.maxConnsPerIP = maxPerIP
	}
}

// CanAccept проверяет, можно ли принять новое SSE-соединение.
func (m *SSEManager) CanAccept(ip string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.maxTotalConns > 0 && m.totalConns >= m.maxTotalConns {
		log.Warn().Int("total", m.totalConns).Int("limit", m.maxTotalConns).Msg("SSE: total connections limit reached")
		return false
	}
	if m.maxConnsPerIP > 0 && m.connsPerIP[ip] >= m.maxConnsPerIP {
		log.Warn().Str("ip", ip).Int("count", m.connsPerIP[ip]).Int("limit", m.maxConnsPerIP).Msg("SSE: per-IP limit reached")
		return false
	}
	return true
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
		log.Debug().Err(err).Msg("SSE: toJSON marshal error")
		return "{}"
	}
	return string(data)
}

// RegisterSession добавляет новое SSE-подключение для игры
func (m *SSEManager) RegisterSession(gameID uint, ip string, w http.ResponseWriter, flush http.Flusher) *SSESession {
	m.mu.Lock()
	defer m.mu.Unlock()

	session := &SSESession{w: w, flush: flush, done: make(chan struct{}), remoteIP: ip}
	m.sessions[gameID] = append(m.sessions[gameID], session)
	if m.gameMap == nil {
		m.gameMap = make(map[*SSESession]uint)
	}
	m.gameMap[session] = gameID
	m.totalConns++
	m.connsPerIP[ip]++
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
			session.closeOnce.Do(func() { close(session.done) })
			break
		}
	}
	if len(m.sessions[gameID]) == 0 {
		delete(m.sessions, gameID)
	}
	if m.totalConns > 0 {
		m.totalConns--
	}
	ip := session.remoteIP
	if count, exists := m.connsPerIP[ip]; exists && count > 0 {
		if count == 1 {
			delete(m.connsPerIP, ip)
		} else {
			m.connsPerIP[ip] = count - 1
		}
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
			s.mu.Lock()
			event := "event: " + eventType + "\ndata: " + toJSON(payload) + "\n\n"
			if _, err := s.w.Write([]byte(event)); err != nil {
				s.mu.Unlock()
				log.Debug().Err(err).Msg("SSE: write error")
				continue
			}
			s.flush.Flush()
			s.mu.Unlock()
		}
	}
}

// SSEHandler возвращает обработчик для SSE-эндпоинта
// SSEHandler возвращает handler для Server-Sent Events.
// @Summary Server-Sent Events для реал-тайм обновлений прохождения
// @Description Подключается к SSE-потоку для получения реал-тайм обновлений статуса прохождения, новых подсказок и завершения уровня
// @Tags gameplay
// @Produce text/event-stream
// @Param passing_id path int true "ID прохождения"
// @Router /game/{passing_id}/sse [get]
// @Security JWT
// sseConnect устанавливает SSE-соединение для указанной игры.
func sseConnect(mgr *SSEManager, c *gin.Context, gameID uint) {
	origin := c.Request.Header.Get("Origin")
	if origin != "" {
		allowed := false
		if c.Request.Host != "" {
			allowed = strings.HasPrefix(origin, "http://"+c.Request.Host) || strings.HasPrefix(origin, "https://"+c.Request.Host)
		}
		if !allowed {
			c.JSON(http.StatusForbidden, gin.H{"error": "origin not allowed"})
			return
		}
	}

	w := c.Writer
	flusher, ok := w.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
		return
	}

	if !mgr.CanAccept(c.ClientIP()) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "слишком много SSE-соединений"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	session := mgr.RegisterSession(gameID, c.ClientIP(), w, flusher)
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

// SSEHandler возвращает handler для SSE по passing_id (геймплей).
func SSEHandler(mgr *SSEManager, db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		passingID, err := strconv.Atoi(c.Param("passing_id"))
		if err != nil || passingID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "неверный passing_id"})
			return
		}
		var passing GamePassing
		if err := db.WithContext(c.Request.Context()).First(&passing, passingID).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "прохождение не найдено"})
			return
		}
		sseConnect(mgr, c, passing.GameID)
	}
}

// SSEGameHandler возвращает handler для SSE по game_id (страница игры).
func SSEGameHandler(mgr *SSEManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		gameID, err := strconv.Atoi(c.Param("game_id"))
		if err != nil || gameID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "неверный game_id"})
			return
		}
		sseConnect(mgr, c, uint(gameID))
	}
}
