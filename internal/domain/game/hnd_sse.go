// internal/domain/game/sse_handler.go
package game

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"gengine-0/internal/pkg/render"

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
	closed    bool // закрыт under s.mu
}

// sseWriteTimeout — таймаут на запись в SSE-соединение (защита от slow-reader DoS).
// Клиент, который перестал читать, блокирует Write() навсегда при WriteTimeout=0.
const sseWriteTimeout = 10 * time.Second

// write записывает данные в SSE-соединение с таймаутом. Вызывается под s.mu.
func (s *SSESession) write(data []byte) error {
	rc := http.NewResponseController(s.w)
	if err := rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout)); err != nil {
		// NewResponseController поддерживается в Go 1.20+ для net/http.
		// Если deadline установить нельзя — пишем без него (старое поведение).
		log.Debug().Err(err).Msg("SSE: SetWriteDeadline failed, writing without deadline")
	}
	_, err := s.w.Write(data)
	if err == nil {
		s.flush.Flush()
	}
	return err
}

// SSEManager управляет SSE-подключениями для каждой игры
type SSEManager struct {
	mu            sync.RWMutex
	sessions      map[uint][]*SSESession
	gameMap       map[*SSESession]uint
	stopOnce      sync.Once
	stopCh        chan struct{}
	stopped       bool // защищает Broadcast от wg.Add после Stop
	maxTotalConns int
	maxConnsPerIP int
	totalConns    int
	connsPerIP    map[string]int
	wg            sync.WaitGroup
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
	if m.stopped {
		return false
	}
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
		m.mu.Lock()
		m.stopped = true
		close(m.stopCh)
		for _, sessions := range m.sessions {
			for _, s := range sessions {
				s.closeOnce.Do(func() {
					close(s.done)
				})
			}
		}
		m.sessions = make(map[uint][]*SSESession)
		m.gameMap = make(map[*SSESession]uint)
		m.mu.Unlock()
		// Wait for all writers to finish (no new wg.Add after stopped=true under m.mu)
		m.wg.Wait()
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
			session.closeOnce.Do(func() {
				session.mu.Lock()
				session.closed = true
				session.mu.Unlock()
				close(session.done)
			})
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
	// Захватываем mu ДО wg.Add, чтобы не конфликтовать с Stop() (wg.Wait).
	// Проверяем stopped — после Stop() новые Broadcast не регистрируются.
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.wg.Add(1)
	sessions := make([]*SSESession, len(m.sessions[gameID]))
	copy(sessions, m.sessions[gameID])
	m.mu.Unlock()
	defer m.wg.Done()

	payload := map[string]any{
		"type":    eventType,
		"game_id": gameID,
		"data":    data,
		"time":    time.Now().Format(time.RFC3339),
	}

	// Сериализуем payload один раз для всех подписчиков (экономия при N подписчиках).
	payloadJSON := toJSON(payload)
	event := "event: " + eventType + "\ndata: " + payloadJSON + "\n\n"

	for _, s := range sessions {
		if !s.mu.TryLock() {
			log.Warn().Msg("SSE broadcast: skipping busy session")
			continue
		}
		if s.closed {
			s.mu.Unlock()
			continue
		}
		if err := s.write([]byte(event)); err != nil {
			s.mu.Unlock()
			log.Debug().Err(err).Msg("SSE: write error")
			continue
		}
		s.mu.Unlock()
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
		// Точное сравнение host (не prefix-match): http://example.com.evil.com НЕ допускается.
		allowed := false
		if c.Request.Host != "" {
			u, err := url.Parse(origin)
			if err == nil {
				originHost := u.Host
				if oh, _, perr := net.SplitHostPort(u.Host); perr == nil {
					originHost = oh
				}
				reqHost := c.Request.Host
				if rh, _, perr := net.SplitHostPort(c.Request.Host); perr == nil {
					reqHost = rh
				}
				allowed = strings.EqualFold(originHost, reqHost)
			}
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

	log.Info().Uint("game_id", gameID).Str("ip", c.ClientIP()).Msg("SSE: connection opened")

	// Прерываем последующие middleware (sessions, logger) — они не должны писать
	// в уже начатый chunked-ответ (иначе клиент получит ERR_INCOMPLETE_CHUNKED_ENCODING).
	c.Abort()

	session := mgr.RegisterSession(gameID, c.ClientIP(), w, flusher)
	defer mgr.UnregisterSession(session)

	// Соединение закрывается по session.done (при отключении клиента) или
	// по отмене request-контекста (graceful shutdown).
	disconnect := c.Request.Context().Done()

	ticker := time.NewTicker(sseHeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-disconnect:
			log.Debug().Uint("game_id", gameID).Msg("SSE: request context done")
			return
		case <-session.done:
			log.Debug().Uint("game_id", gameID).Msg("SSE: session done")
			return
		case <-mgr.stopCh:
			log.Debug().Uint("game_id", gameID).Msg("SSE: manager stopped")
			return
		case <-ticker.C:
			// Пишем heartbeat под мьютексом сессии — безопасно с Broadcast()
			session.mu.Lock()
			if err := session.write([]byte(": heartbeat\n\n")); err != nil {
				session.mu.Unlock()
				log.Debug().Err(err).Uint("game_id", gameID).Msg("SSE: heartbeat write error")
				return
			}
			session.mu.Unlock()
		}
	}
}

// SSEHandler возвращает handler для SSE по passing_id (геймплей).
func SSEHandler(mgr *SSEManager, db *gorm.DB, coAuthorSvc *CoAuthorService) gin.HandlerFunc {
	return func(c *gin.Context) {
		passingID, err := strconv.Atoi(c.Param("passing_id"))
		if err != nil || passingID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": render.Tr(c, "handler.invalid_passing_id")})
			return
		}
		userID := c.GetUint("userID")
		var passing GamePassing
		if err := db.WithContext(c.Request.Context()).First(&passing, passingID).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": render.Tr(c, "handler.passing_not_found")})
			return
		}
		// Authorization: user must be a team member OR a game manager
		if !isSSEParticipant(db, c.Request.Context(), passing.TeamID, passing.GameID, userID, coAuthorSvc) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": render.Tr(c, "handler.forbidden")})
			return
		}
		sseConnect(mgr, c, passing.GameID)
	}
}

// SSEGameHandler возвращает handler для SSE по game_id (страница игры).
func SSEGameHandler(mgr *SSEManager, db *gorm.DB, coAuthorSvc *CoAuthorService) gin.HandlerFunc {
	return func(c *gin.Context) {
		gameID, err := strconv.Atoi(c.Param("game_id"))
		if err != nil || gameID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": render.Tr(c, "handler.invalid_game_id")})
			return
		}
		userID := c.GetUint("userID")
		// Authorization: user must be a game manager (game page SSE exposes team data)
		if ok, authErr := coAuthorSvc.IsUserManager(c.Request.Context(), uint(gameID), userID); authErr != nil || !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": render.Tr(c, "handler.forbidden")})
			return
		}
		sseConnect(mgr, c, uint(gameID))
	}
}

// isSSEParticipant проверяет, что пользователь участвует в прохождении (команда)
// или является менеджером игры.
func isSSEParticipant(db *gorm.DB, ctx context.Context, teamID, gameID, userID uint, coAuthorSvc *CoAuthorService) bool {
	// Game manager always allowed
	if ok, err := coAuthorSvc.IsUserManager(ctx, gameID, userID); err == nil && ok {
		return true
	}
	// Team member check
	var count int64
	if err := db.WithContext(ctx).Table("team_members").
		Where("team_id = ? AND user_id = ?", teamID, userID).
		Count(&count).Error; err != nil {
		log.Debug().Err(err).Uint("team_id", teamID).Uint("user_id", userID).Msg("isSSEParticipant: member check error")
		return false
	}
	return count > 0
}
