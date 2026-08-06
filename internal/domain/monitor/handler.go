// internal/domain/monitor/handler.go
package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/user"
	apperrors "gengine-0/internal/pkg/errors"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/sanitize"
	"gengine-0/internal/pkg/validation"
	ws "gengine-0/internal/pkg/websocket"

	csrf "gengine-0/internal/pkg/csrf"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return false
		}
		host := r.Host
		// NB: X-Forwarded-Host не доверяем — при прямом доступе атакующий
		// подделывает и Origin, и X-Forwarded-Host, обходя проверку.
		// Exact host match (with port normalization) — NOT prefix match
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		originHost := u.Host
		if oHost, _, pErr := net.SplitHostPort(u.Host); pErr == nil {
			originHost = oHost
		}
		reqHost := host
		if rHost, _, pErr := net.SplitHostPort(host); pErr == nil {
			reqHost = rHost
		}
		return strings.EqualFold(originHost, reqHost)
	},
}

// ---------- Входные структуры для валидации ----------

type GameIDRequest struct {
	ID uint `uri:"id" binding:"required,gt=0"`
}

type GameIDAndSessionIDRequest struct {
	ID        uint `uri:"id" binding:"required,gt=0"`
	SessionID uint `uri:"session_id" binding:"required,gt=0"`
}

type SessionIDRequest struct {
	SessionID uint `uri:"session_id" binding:"required,gt=0"`
}

type StartVotingInput struct {
	PassingID uint `form:"passing_id" binding:"required,gt=0"`
	LevelID   uint `form:"level_id" binding:"required,gt=0"`
}

type VoteInput struct {
	SessionID uint   `form:"session_id" binding:"required,gt=0"`
	TeamID    uint   `form:"team_id" binding:"required,gt=0"`
	Option    string `form:"option" binding:"required"`
}

// ---------- Обработчики ----------

// Shared poller for monitor SSE — один сборщик на игру вместо N клиентов × 1 запрос/сек
type monitorSubscriber struct {
	ch   chan []byte
	done chan struct{}
}

var (
	monitorPollers   = make(map[uint]*monitorGamePoller)
	monitorPollersMu sync.Mutex
)

// wsMessageLimiter — простой per-connection token bucket для WS-сообщений.
// Защищает от спама через один сокет (нет глобального состояния, GC-safe).
type wsMessageLimiter struct {
	mu    sync.Mutex
	limit int
	// Скользящее окно: храним времена последних сообщений.
	window time.Duration
	times  []time.Time
}

// Allow возвращает true, если сообщение можно принять.
func (l *wsMessageLimiter) Allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-l.window)

	// Отбрасываем записи старше окна.
	kept := l.times[:0]
	for _, t := range l.times {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	l.times = kept

	if len(l.times) >= l.limit {
		return false
	}
	l.times = append(l.times, now)
	return true
}

type monitorGamePoller struct {
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	subscribers []*monitorSubscriber
	subMu       sync.Mutex
	// lastData — последний отправленный payload (P-M3): идентичные снапшоты
	// не рассылаем повторно каждую секунду.
	lastData []byte
}

// subscribeMonitor добавляет подписчика для указанной игры. Если сборщик не запущен — запускает его.
func subscribeMonitor(gameID uint, snapFn func(context.Context) ([]byte, error)) *monitorSubscriber {
	monitorPollersMu.Lock()
	defer monitorPollersMu.Unlock()

	sub := &monitorSubscriber{
		ch:   make(chan []byte, 4),
		done: make(chan struct{}),
	}

	poller, exists := monitorPollers[gameID]
	if !exists {
		ctx, cancel := context.WithCancel(context.Background())
		poller = &monitorGamePoller{cancel: cancel}
		monitorPollers[gameID] = poller

		poller.wg.Add(1)
		go func() {
			defer poller.wg.Done()
			ticker := time.NewTicker(1 * time.Second)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					data, err := snapFn(ctx)
					if err != nil {
						continue
					}
					poller.subMu.Lock()
					// P-M3: снапшот не изменился — не рассылаем дубли.
					if bytes.Equal(poller.lastData, data) {
						poller.subMu.Unlock()
						continue
					}
					poller.lastData = data
					for _, s := range poller.subscribers {
						select {
						case s.ch <- data:
						default:
							// subscriber too slow, skip
						}
					}
					poller.subMu.Unlock()
				}
			}
		}()
	}

	poller.subMu.Lock()
	poller.subscribers = append(poller.subscribers, sub)
	// M2: новый подписчик сразу получает текущий снапшот (если он есть),
	// иначе при неизменных данных он ждал бы следующего изменения состояния.
	if poller.lastData != nil {
		select {
		case sub.ch <- poller.lastData:
		default:
		}
	}
	poller.subMu.Unlock()

	return sub
}

// StopAllMonitorPollers останавливает все активные сборщики (вызывается при graceful shutdown).
// Не держит глобальный мьютекс во время wg.Wait, чтобы не блокировать другие игры.
func StopAllMonitorPollers() {
	monitorPollersMu.Lock()
	pollers := make([]*monitorGamePoller, 0, len(monitorPollers))
	for id, poller := range monitorPollers {
		poller.cancel()
		delete(monitorPollers, id)
		pollers = append(pollers, poller)
	}
	monitorPollersMu.Unlock()

	for _, poller := range pollers {
		poller.wg.Wait()
	}
}

// unsubscribeMonitor удаляет подписчика. Если подписчиков не осталось — останавливает сборщик.
func unsubscribeMonitor(gameID uint, sub *monitorSubscriber) {
	monitorPollersMu.Lock()
	poller, exists := monitorPollers[gameID]
	if !exists {
		monitorPollersMu.Unlock()
		return
	}

	// M1: держим глобальный мьютекс на время удаления подписчика и решения
	// об удалении poller из map. subscribeMonitor тоже держит его при append,
	// поэтому конкурентный subscribe либо увидит poller (remaining>0) и
	// прицепится к живому, либо не найдёт его (remaining==0) и создаст новый.
	// Раньше окно между cancel() и delete(monitorPollers) позволяло прицепить
	// подписчика к отменённому сборщику — тот висел без данных.
	poller.subMu.Lock()
	for i, s := range poller.subscribers {
		if s == sub {
			poller.subscribers = append(poller.subscribers[:i], poller.subscribers[i+1:]...)
			close(sub.done)
			break
		}
	}
	remaining := len(poller.subscribers)
	if remaining == 0 {
		delete(monitorPollers, gameID)
	}
	poller.subMu.Unlock()
	monitorPollersMu.Unlock()

	if remaining == 0 {
		poller.cancel()
		// Wait outside the global mutex to avoid blocking other games
		poller.wg.Wait()
	}
}

type MonitorHandler struct {
	monitorService      *game.MonitorService
	blackboxVoteService *BlackboxVoteService
	chatService         *ChatService
	hub                 *ws.RoomHub
	userService         *user.UserService
	gameService         *game.GameService
	coAuthorSvc         *game.CoAuthorService
}

func NewMonitorHandler(
	monitorSvc *game.MonitorService,
	voteSvc *BlackboxVoteService,
	chatSvc *ChatService,
	hub *ws.RoomHub,
	userSvc *user.UserService,
	gameSvc *game.GameService,
	coAuthorSvc *game.CoAuthorService,
) *MonitorHandler {
	return &MonitorHandler{
		monitorService:      monitorSvc,
		blackboxVoteService: voteSvc,
		chatService:         chatSvc,
		hub:                 hub,
		userService:         userSvc,
		gameService:         gameSvc,
		coAuthorSvc:         coAuthorSvc,
	}
}

// MonitorPage отображает HTML-страницу мониторинга.
// @Summary Страница мониторинга игры
// @Tags monitor
// @Produce html
// @Param id path int true "ID игры"
// @Success 200 {string} html "Страница мониторинга"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
// @Router /games/{id}/monitor [get]
// @Security JWT
func (h *MonitorHandler) MonitorPage(c *gin.Context) {
	var req GameIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_game_id"))
		return
	}

	userID := c.GetUint("userID")
	isAdmin := middleware.IsAdmin(c)

	render.Page(c, http.StatusOK, "monitor-page.html", gin.H{
		"Title":         render.Tr(c, "nav.monitor"),
		"GameID":        req.ID,
		"csrf":          csrf.GetToken(c),
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
		"Breadcrumbs": []map[string]string{
			{"name": "nav.home", "url": "/"},
			{"name": "nav.games", "url": "/games"},
			{"name": "game.breadcrumb_label", "url": "/games/" + c.Param("id")},
			{"name": "nav.monitor"},
		},
	})
}

// MonitorData возвращает snapshot прогресса игры в JSON (для polling fallback).
// @Summary Данные мониторинга (JSON)
// @Tags monitor
// @Produce json
// @Param id path int true "ID игры"
// @Success 200 {object} map[string]interface{} "Snapshot прогресса команд"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
// @Router /games/{id}/monitor/data [get]
// @Security JWT
func (h *MonitorHandler) MonitorData(c *gin.Context) {
	var req GameIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": render.Tr(c, "handler.invalid_game_id")})
		return
	}

	snapshot, err := h.monitorService.GetOrFetchSnapshot(c.Request.Context(), req.ID)
	if err != nil {
		log.Error().Err(err).Uint("game_id", req.ID).Msg("MonitorWS: failed to get snapshot")
		c.JSON(http.StatusInternalServerError, gin.H{"error": render.Tr(c, "handler.snapshot_failed")})
		return
	}

	c.JSON(http.StatusOK, gin.H{"teams": snapshot})
}

// MonitorStreamSSE предоставляет Server-Sent Events для обновлений прогресса игры.
// @Summary Поток мониторинга (SSE)
// @Tags monitor
// @Produce text/event-stream
// @Param id path int true "ID игры"
// @Success 200 {string} string "SSE поток обновлений"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
// @Router /games/{id}/monitor/stream [get]
// @Security JWT
func (h *MonitorHandler) MonitorStreamSSE(c *gin.Context) {
	var req GameIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid game ID"})
		return
	}
	gameID := req.ID

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	// Subscribe to shared poller — один сборщик на игру вместо N клиентов × 1 запрос/сек
	snapFn := func(ctx context.Context) ([]byte, error) {
		snapshot, err := h.monitorService.GetOrFetchSnapshot(ctx, gameID)
		if err != nil {
			return nil, err
		}
		return json.Marshal(snapshot)
	}
	sub := subscribeMonitor(gameID, snapFn)
	defer unsubscribeMonitor(gameID, sub)

	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()

	ctx := c.Request.Context()

	for {
		select {
		case <-ctx.Done():
			log.Debug().Uint("game_id", gameID).Msg("SSE connection closed by client")
			return
		case <-sub.done:
			return
		case <-pingTicker.C:
			if _, err := fmt.Fprintf(c.Writer, ": ping\n\n"); err != nil {
				log.Debug().Err(err).Uint("game_id", gameID).Msg("SSE ping write error")
				return
			}
			c.Writer.Flush()
		case data := <-sub.ch:
			if _, writeErr := fmt.Fprintf(c.Writer, "event: update\ndata: %s\n\n", data); writeErr != nil {
				log.Debug().Err(writeErr).Uint("game_id", gameID).Msg("SSE write error")
				return
			}
			c.Writer.Flush()
		}
	}
}

// MonitorWS обрабатывает WebSocket-соединение для live-обновлений прогресса.
// @Summary WebSocket мониторинга
// @Tags monitor
// @Param id path int true "ID игры"
// @Success 101 {string} string "Switching Protocols"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
// @Failure 429 {object} map[string]interface{} "Слишком много соединений"
// @Router /games/{id}/monitor/ws [get]
// @Security JWT
func (h *MonitorHandler) MonitorWS(c *gin.Context) {
	var req GameIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		log.Warn().Err(err).Msg("MonitorWS: invalid game ID")
		return
	}
	gameID := strconv.Itoa(int(req.ID))
	remoteIP := c.ClientIP()

	// 🔒 P1-2: Проверка аутентификации перед WebSocket-соединением
	userID := c.GetUint("userID")
	if userID == 0 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": render.Tr(c, "handler.unauthorized")})
		return
	}

	// 🔒 Проверка прав доступа к игре (автор, соавтор или модератор)
	ok, err := h.coAuthorSvc.IsUserManager(c.Request.Context(), req.ID, userID)
	if err != nil || !ok {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": render.Tr(c, "handler.monitor_denied")})
		return
	}

	if !h.hub.CanAccept(remoteIP) {
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"error": "слишком много активных WebSocket-соединений",
		})
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Error().Err(err).Str("game_id", gameID).Msg("MonitorWS: failed to upgrade connection")
		return
	}
	client := ws.NewClient(conn, gameID, remoteIP)
	h.hub.RegisterClient(client)
	c.Abort()

	snapshot, err := h.monitorService.GetOrFetchSnapshot(c.Request.Context(), req.ID)
	if err != nil {
		log.Error().Err(err).Uint("game_id", req.ID).Msg("MonitorWS: failed to get snapshot")
	} else {
		if data, err := json.Marshal(snapshot); err == nil {
			// Неблокирующая отправка: если буфер клиента переполнен (write pump ещё не
			// стартовал или клиент медленный) — дропаем снапшот, не блокируя хендлер (M4).
			select {
			case client.Send <- data:
			default:
				log.Warn().Str("game_id", gameID).Msg("MonitorWS: client buffer full, dropping snapshot")
			}
		} else {
			log.Error().Err(err).Uint("game_id", req.ID).Msg("MonitorWS: failed to marshal snapshot")
		}
	}

	// WebSocket lives beyond the HTTP handler — use a background context that is
	// cancelled only when the connection goroutine finishes, NOT when the handler returns.
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		defer func() {
			cancel()
			h.hub.UnregisterClient(client)
			client.Close()
		}()
		ws.HandleWebSocketWithContext(ctx, client)
	}()
}

// ChatPage отображает HTML-страницу чата игры.
// @Summary Страница чата игры
// @Tags monitor
// @Produce html
// @Param id path int true "ID игры"
// @Success 200 {string} html "Страница чата"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /games/{id}/chat [get]
// @Security JWT
func (h *MonitorHandler) ChatPage(c *gin.Context) {
	var req GameIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_game_id"))
		return
	}
	gameID := req.ID
	userID := c.GetUint("userID")

	ctx := c.Request.Context()
	userName := "Вы"
	if u, err := h.userService.GetByID(ctx, userID); err == nil {
		userName = sanitize.StripHTML(u.Name)
	}

	var passingID *uint
	var teamID *uint

	if p, err := h.gameService.GetPassingByUser(ctx, gameID, userID); err == nil {
		pID := p.ID
		tID := p.TeamID
		passingID = &pID
		teamID = &tID
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.Error().Err(err).Uint("game_id", gameID).Uint("user_id", userID).Msg("ChatPage: failed to find passing")
	}

	isAdmin := middleware.IsAdmin(c)

	render.Page(c, http.StatusOK, "chat-page.html", gin.H{
		"Title":         render.Tr(c, "nav.chat"),
		"GameID":        gameID,
		"PassingID":     passingID,
		"TeamID":        teamID,
		"UserID":        userID,
		"UserName":      userName,
		"csrf":          csrf.GetToken(c),
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
		"Breadcrumbs": []map[string]string{
			{"name": "nav.home", "url": "/"},
			{"name": "nav.games", "url": "/games"},
			{"name": "game.breadcrumb_label", "url": "/games/" + c.Param("id")},
			{"name": "nav.chat"},
		},
	})
}

// ChatWS обрабатывает WebSocket-соединение чата.
// @Summary WebSocket чата
// @Tags monitor
// @Param room query string true "ID комнаты чата"
// @Success 101 {string} string "Switching Protocols"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 429 {object} map[string]interface{} "Слишком много соединений"
// @Router /chat/ws [get]
// @Security JWT
func (h *MonitorHandler) ChatWS(c *gin.Context) {
	roomID := c.Query("room")
	if roomID == "" {
		log.Warn().Msg("ChatWS: missing room parameter")
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	userID := c.GetUint("userID")
	if userID == 0 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": render.Tr(c, "handler.unauthorized")})
		return
	}
	remoteIP := c.ClientIP()

	if !h.hub.CanAccept(remoteIP) {
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"error": "слишком много активных WebSocket-соединений",
		})
		return
	}

	roomIDUint, err := strconv.Atoi(roomID)
	if err != nil || roomIDUint <= 0 {
		log.Warn().Str("room_id", roomID).Msg("ChatWS: invalid room ID")
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	// Проверка прав доступа к комнате чата
	chatRoom, findErr := h.chatService.GetByID(c.Request.Context(), uint(roomIDUint))
	if findErr != nil {
		log.Warn().Err(findErr).Str("room_id", roomID).Msg("ChatWS: room not found")
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "комната не найдена"})
		return
	}
	if chatRoom.GameID != nil {
		isManager, mgrErr := h.coAuthorSvc.IsUserManager(c.Request.Context(), *chatRoom.GameID, userID)
		if mgrErr != nil {
			log.Error().Err(mgrErr).Uint("game_id", *chatRoom.GameID).Uint("user_id", userID).Msg("ChatWS: manager check error")
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "внутренняя ошибка"})
			return
		}
		if !isManager {
			_, findErr := h.gameService.GetPassingByUser(c.Request.Context(), *chatRoom.GameID, userID)
			if findErr != nil {
				log.Warn().Uint("user_id", userID).Uint("game_id", *chatRoom.GameID).Msg("ChatWS: access denied, not a participant")
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": render.Tr(c, "handler.forbidden")})
				return
			}
		}
	} else if chatRoom.TeamID != nil {
		// Проверка доступа к командному чату (участник или капитан).
		ok, memberErr := h.chatService.IsTeamMemberOrCaptain(c.Request.Context(), *chatRoom.TeamID, userID)
		if memberErr != nil || !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": render.Tr(c, "handler.forbidden")})
			return
		}
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Error().Err(err).Str("room_id", roomID).Msg("ChatWS: failed to upgrade connection")
		return
	}
	// После успешного апгрейда запрещаем дальнейшую запись в ответ
	c.Abort()

	client := ws.NewClient(conn, roomID, remoteIP)
	h.hub.RegisterClient(client)
	defer func() {
		h.hub.UnregisterClient(client)
		client.Close()
	}()

	// Create background context for all post-upgrade DB operations
	wsCtx, wsCancel := context.WithCancel(context.Background())
	defer wsCancel()

	msgs, err := h.chatService.GetMessages(wsCtx, uint(roomIDUint), 50)
	if err != nil {
		log.Error().Err(err).Int("room_id", roomIDUint).Msg("ChatWS: failed to get history")
	} else {
		for i := range msgs {
			msgs[i].Content = sanitize.StripHTML(msgs[i].Content)
			if msgs[i].User.Name != "" {
				msgs[i].User.Name = sanitize.StripHTML(msgs[i].User.Name)
			}
		}
		data, err := json.Marshal(gin.H{"type": "history", "messages": msgs})
		if err == nil {
			// Неблокирующая отправка (M4): если буфер переполнен — дропаем историю.
			select {
			case client.Send <- data:
			default:
				log.Warn().Int("room_id", roomIDUint).Msg("ChatWS: client buffer full, dropping history")
			}
		} else {
			log.Error().Err(err).Int("room_id", roomIDUint).Msg("ChatWS: failed to marshal history")
		}
	}

	go ws.WritePumpWithContext(wsCtx, client)

	// M5: per-connection rate limit — не более 10 сообщений за 5 секунд
	// (token bucket, защита от спама/DoS через один сокет).
	msgLimiter := &wsMessageLimiter{limit: 10, window: 5 * time.Second}

	// Set read deadline and pong handler to prevent goroutine leaks on client disconnect
	conn.SetReadLimit(32 * 1024)
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	})

	for {
		select {
		case <-wsCtx.Done():
			log.Debug().Str("room_id", roomID).Msg("ChatWS: context cancelled, stopping read loop")
			return
		default:
			if err := conn.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
				log.Debug().Err(err).Str("room_id", roomID).Msg("ChatWS: set read deadline failed")
			}
			_, message, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Debug().Err(err).Str("room_id", roomID).Msg("ChatWS: read error")
				}
				return
			}
			// Client sends JSON: {"type":"message","room_id":...,"content":"..."}
			var msgData struct {
				Content string `json:"content"`
			}
			if parseErr := json.Unmarshal(message, &msgData); parseErr != nil || msgData.Content == "" {
				msgData.Content = string(message)
			}
			cleanContent := sanitize.StripHTML(msgData.Content)
			if cleanContent == "" {
				continue
			}
			if !msgLimiter.Allow() {
				log.Warn().Str("room_id", roomID).Uint("user_id", userID).Msg("ChatWS: message rate limit exceeded")
				continue
			}
			msg, err := h.chatService.SaveMessage(wsCtx, uint(roomIDUint), userID, cleanContent)
			if err != nil {
				log.Error().Err(err).Str("room_id", roomID).Uint("user_id", userID).Msg("ChatWS: failed to save message")
				continue
			}
			msg.Content = sanitize.StripHTML(msg.Content)
			if msg.User.Name != "" {
				msg.User.Name = sanitize.StripHTML(msg.User.Name)
			}
			resp, err := json.Marshal(gin.H{"type": "message", "message": msg})
			if err != nil {
				log.Error().Err(err).Uint("msg_id", msg.ID).Msg("ChatWS: failed to marshal message")
				continue
			}
			h.hub.BroadcastToRoom(roomID, resp)
		}
	}
}

// ChatRoomIDs возвращает ID комнат чата (общая и командная) для игры.
// @Summary ID комнат чата
// @Tags monitor
// @Produce json
// @Param id path int true "ID игры"
// @Success 200 {object} map[string]interface{} "ID комнат чата"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /games/{id}/chat-rooms [get]
// @Security JWT
func (h *MonitorHandler) ChatRoomIDs(c *gin.Context) {
	var req GameIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		appErr := apperrors.BadRequest(render.Tr(c, "handler.invalid_game_id"))
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}
	gameID := req.ID
	userID := c.GetUint("userID")

	ctx := c.Request.Context()

	// Доступ к комнатам чата только для менеджеров игры или её участников —
	// иначе любой авторизованный пользователь может создавать комнаты
	// для произвольного gameID (spam-создание сущностей).
	isManager, mgrErr := h.coAuthorSvc.IsUserManager(ctx, gameID, userID)
	if mgrErr != nil {
		log.Error().Err(mgrErr).Uint("game_id", gameID).Uint("user_id", userID).Msg("ChatRoomIDs: manager check error")
		appErr := apperrors.Wrap(mgrErr, "MonitorHandler")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{"error": appErr.Message, "code": appErr.Code})
		return
	}
	if !isManager {
		if _, err := h.gameService.GetPassingByUser(ctx, gameID, userID); err != nil {
			log.Warn().Uint("game_id", gameID).Uint("user_id", userID).Msg("ChatRoomIDs: access denied, not a participant")
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": render.Tr(c, "handler.forbidden"), "code": "forbidden"})
			return
		}
	}

	generalRoom, err := h.chatService.GetOrCreateGameRoom(ctx, gameID)
	if err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("ChatRoomIDs: failed to get general room")
		appErr := apperrors.Wrap(err, "MonitorHandler")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	var teamRoom *ChatRoom
	passing, findErr := h.gameService.GetPassingByUser(ctx, gameID, userID)
	if findErr == nil {
		room, roomErr := h.chatService.GetOrCreateTeamRoom(ctx, gameID, passing.TeamID, passing.ID)
		if roomErr != nil {
			log.Error().Err(roomErr).Uint("game_id", gameID).Uint("team_id", passing.TeamID).Msg("ChatRoomIDs: failed to get team room")
		} else {
			teamRoom = room
		}
	} else if !errors.Is(findErr, gorm.ErrRecordNotFound) {
		log.Error().Err(findErr).Uint("game_id", gameID).Uint("user_id", userID).Msg("ChatRoomIDs: failed to find passing")
	}

	resp := gin.H{
		"general_room_id": generalRoom.ID,
	}
	if teamRoom != nil {
		resp["team_room_id"] = teamRoom.ID
	} else {
		resp["team_room_id"] = 0
	}

	c.JSON(http.StatusOK, resp)
}

// ListLogs отображает HTML-страницу с историей логов игры.
// @Summary Логи игры
// @Tags monitor
// @Produce html
// @Param id path int true "ID игры"
// @Success 200 {string} html "Страница логов"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
// @Router /games/{id}/logs [get]
// @Security JWT
func (h *MonitorHandler) ListLogs(c *gin.Context) {
	var req GameIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_game_id"))
		return
	}
	gameID := req.ID

	// Пагинация (P-M5): не грузим все логи игры (поддерживается шаблоном).
	page, perPage := 1, 50
	if p, err := strconv.Atoi(c.Query("page")); err == nil && p > 0 {
		page = p
	}
	if pp, err := strconv.Atoi(c.Query("per_page")); err == nil && pp > 0 {
		perPage = pp
	}
	// M4: сервис ограничивает pageSize до 100 — считаем totalPages по
	// скорректированному значению, иначе UI показывает неверное число страниц.
	if perPage > 100 {
		perPage = 100
	}

	logs, total, err := h.gameService.GetLogsByGameIDPaginated(c.Request.Context(), gameID, page, perPage)
	if err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("ListLogs: failed to fetch logs")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	totalPages := (int(total) + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}
	render.Page(c, http.StatusOK, "logs-list.html", gin.H{
		"Title":      "Журнал",
		"GameID":     gameID,
		"Logs":       logs,
		"Page":       page,
		"TotalPages": totalPages,
		"csrf":       csrf.GetToken(c),
	})
}

// LogsWS предоставляет WebSocket-стрим логов игры.
// @Summary WebSocket логов
// @Tags monitor
// @Param id path int true "ID игры"
// @Success 101 {string} string "Switching Protocols"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
// @Failure 429 {object} map[string]interface{} "Слишком много соединений"
// @Router /games/{id}/logs/ws [get]
// @Security JWT
func (h *MonitorHandler) LogsWS(c *gin.Context) {
	var req GameIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		log.Warn().Err(err).Msg("LogsWS: invalid game ID")
		return
	}
	userID := c.GetUint("userID")
	if userID == 0 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": render.Tr(c, "handler.unauthorized")})
		return
	}

	if ok, err := h.coAuthorSvc.IsUserManager(c.Request.Context(), req.ID, userID); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Ошибка проверки прав"})
		return
	} else if !ok {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": render.Tr(c, "handler.forbidden")})
		return
	}

	gameID := strconv.Itoa(int(req.ID))
	remoteIP := c.ClientIP()

	if !h.hub.CanAccept(remoteIP) {
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"error": "слишком много активных WebSocket-соединений",
		})
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Error().Err(err).Str("game_id", gameID).Msg("LogsWS: failed to upgrade connection")
		return
	}
	client := ws.NewClient(conn, "logs_"+gameID, remoteIP)
	h.hub.RegisterClient(client)
	c.Abort()

	// WebSocket lives beyond the HTTP handler — cancel only when the goroutine finishes
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		defer func() {
			cancel()
			h.hub.UnregisterClient(client)
			client.Close()
		}()
		ws.HandleWebSocketWithContext(ctx, client)
	}()
}

// StartVoting запускает голосование по текущему уровню-чёрному ящику.
// @Summary Запуск голосования
// @Tags monitor
// @Accept x-www-form-urlencoded
// @Produce json
// @Param passing_id formData uint true "ID прохождения"
// @Param level_id formData uint true "ID уровня"
// @Success 200 {object} map[string]interface{} "Голосование запущено"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
// @Router /voting/start [post]
// @Security JWT
func (h *MonitorHandler) StartVoting(c *gin.Context) {
	var input StartVotingInput
	if err := c.ShouldBind(&input); err != nil {
		appErr := apperrors.BadRequest(render.LocalizeError(c, err.Error()))
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	if err := validation.ValidatePositiveUint("ID прохождения", input.PassingID); err != nil {
		appErr := apperrors.BadRequest(render.LocalizeError(c, err.Error()))
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}
	if err := validation.ValidatePositiveUint("ID уровня", input.LevelID); err != nil {
		appErr := apperrors.BadRequest(render.LocalizeError(c, err.Error()))
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	userID := c.GetUint("userID")
	if err := h.blackboxVoteService.StartVoting(c.Request.Context(), input.PassingID, input.LevelID, userID); err != nil {
		switch err.Error() {
		case "голосование уже активно", "голосование уже было проведено":
			appErr := apperrors.BadRequest(render.LocalizeError(c, err.Error()))
			c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{"error": appErr.Message, "code": appErr.Code})
		default:
			log.Error().Err(err).Uint("passing_id", input.PassingID).Uint("level_id", input.LevelID).Uint("user_id", userID).Msg("StartVoting: failed to start voting")
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": render.Tr(c, "handler.internal_error"), "code": "internal_error"})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "Голосование запущено"})
}

// Vote обрабатывает голос команды за выбранный вариант.
// @Summary Голосование
// @Tags monitor
// @Accept x-www-form-urlencoded
// @Produce json
// @Param session_id formData uint true "ID сессии голосования"
// @Param team_id formData uint true "ID команды"
// @Param option formData string true "Выбранный вариант"
// @Success 200 {object} map[string]interface{} "Голос учтён"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
// @Router /voting/vote [post]
// @Security JWT
func (h *MonitorHandler) Vote(c *gin.Context) {
	var input VoteInput
	if err := c.ShouldBind(&input); err != nil {
		appErr := apperrors.BadRequest(render.LocalizeError(c, err.Error()))
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	if err := validation.ValidatePositiveUint("ID сессии", input.SessionID); err != nil {
		appErr := apperrors.BadRequest(render.LocalizeError(c, err.Error()))
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}
	if err := validation.ValidatePositiveUint("ID команды", input.TeamID); err != nil {
		appErr := apperrors.BadRequest(render.LocalizeError(c, err.Error()))
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}
	if err := validation.ValidateString("Вариант ответа", input.Option, 1, 1000); err != nil {
		appErr := apperrors.BadRequest(render.LocalizeError(c, err.Error()))
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	cleanOption := sanitize.StripHTML(input.Option)
	userID := c.GetUint("userID")

	if err := h.blackboxVoteService.Vote(c.Request.Context(), input.SessionID, input.TeamID, userID, cleanOption); err != nil {
		switch err.Error() {
		case "голосование закрыто", "недопустимый вариант ответа", "ваш голос уже учтён", "вы не являетесь участником этой команды":
			appErr := apperrors.BadRequest(render.LocalizeError(c, err.Error()))
			c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{"error": appErr.Message, "code": appErr.Code})
		default:
			log.Error().Err(err).Uint("session_id", input.SessionID).Uint("team_id", input.TeamID).Str("option", cleanOption).Msg("Vote: failed to vote")
			appErr := apperrors.Wrap(err, "MonitorHandler")
			c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{"error": appErr.Message, "code": appErr.Code})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "Голос учтён"})
}

// GetVotingResults возвращает текущие результаты голосования.
// @Summary Результаты голосования
// @Tags monitor
// @Produce json
// @Param session_id path int true "ID сессии голосования"
// @Success 200 {object} map[string]interface{} "Результаты голосования"
// @Failure 400 {object} map[string]interface{} render.Tr(c, "handler.invalid_session_id")
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /voting/{session_id}/results [get]
// @Security JWT
func (h *MonitorHandler) GetVotingResults(c *gin.Context) {
	var req SessionIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		appErr := apperrors.BadRequest(render.Tr(c, "handler.invalid_session_id"))
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}
	userID := c.GetUint("userID")
	if userID == 0 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": render.Tr(c, "handler.unauthorized")})
		return
	}
	results, err := h.blackboxVoteService.GetVotingResults(c.Request.Context(), req.SessionID, userID)
	if err != nil {
		log.Error().Err(err).Uint("session_id", req.SessionID).Msg("GetVotingResults: failed to get results")
		appErr := apperrors.Wrap(err, "MonitorHandler")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"results": results})
}

// CloseVoting завершает голосование и определяет победителя.
// @Summary Закрытие голосования
// @Tags monitor
// @Accept x-www-form-urlencoded
// @Produce json
// @Param session_id path int true "ID сессии голосования"
// @Success 200 {object} map[string]interface{} "Победивший вариант"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
// @Router /voting/{session_id}/close [post]
// @Security JWT
func (h *MonitorHandler) CloseVoting(c *gin.Context) {
	var req SessionIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		appErr := apperrors.BadRequest(render.Tr(c, "handler.invalid_session_id"))
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}
	userID := c.GetUint("userID")

	winner, err := h.blackboxVoteService.CloseVoting(c.Request.Context(), req.SessionID, userID)
	if err != nil {
		log.Error().Err(err).Uint("session_id", req.SessionID).Uint("user_id", userID).Msg("CloseVoting: failed to close voting")
		appErr := apperrors.Forbidden(render.LocalizeError(c, err.Error()))
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"winner": sanitize.StripHTML(winner)})
}
