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
			// F-3 (pass 35): 5с вместо 1с — GetOrFetchSnapshot кэширует снапшот
			// на 30с, значит реальный запрос к БД всё равно не чаще раза в 30с,
			// а пустые вызовы snapFn сокращаются в 5 раз (60 → 12 вызовов/мин).
			ticker := time.NewTicker(5 * time.Second)
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

	// chatRooms (IDEA-6): комнаты, подключённые через ChatWS. Колбэк presence
	// хаба срабатывает для ВСЕХ комнат (включая монитор/логи), поэтому фильтруем:
	// presence рассылается только в комнаты, помеченные здесь.
	chatRooms sync.Map // roomID(string) → struct{}
}

// markChatRoom помечает комнату как чат (вызывается из ChatWS).
func (h *MonitorHandler) markChatRoom(roomID string) {
	h.chatRooms.Store(roomID, struct{}{})
}

// unmarkChatRoom снимает пометку при отключении последнего клиента.
func (h *MonitorHandler) unmarkChatRoom(roomID string) {
	h.chatRooms.Delete(roomID)
}

// setupChatPresence подключает presence-онлайн-индикатор (IDEA-6): при
// изменении состава чат-комнаты в неё рассылается {type:"presence", count, user_ids}.
func (h *MonitorHandler) setupChatPresence() {
	h.hub.SetOnRoomChange(func(roomID string) {
		if _, ok := h.chatRooms.Load(roomID); !ok {
			return
		}
		count := h.hub.RoomClientCount(roomID)
		userIDs := h.hub.RoomUserIDs(roomID)
		payload, err := json.Marshal(gin.H{
			"type":     "presence",
			"count":    count,
			"user_ids": userIDs,
			"room_id":  roomID,
		})
		if err != nil {
			return
		}
		h.hub.BroadcastToRoom(roomID, payload)
	})
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
		"Title":          render.Tr(c, "nav.monitor"),
		"GameID":         req.ID,
		"csrf":           csrf.GetToken(c),
		"CurrentUserID":  userID,
		"IsAdmin":        isAdmin,
		"IncludeLeaflet": true, // G-3 (pass 45): карта позиций водителей
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
	// F-2 (pass 36): GetOrFetchSnapshotJSON кэширует маршалнутые байты —
	// json.Marshal больше не выполняется каждый тик поллера.
	snapFn := func(ctx context.Context) ([]byte, error) {
		return h.monitorService.GetOrFetchSnapshotJSON(ctx, gameID)
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

	// DEEP-REVIEW P11 (pass 46): GetOrFetchSnapshotJSON — сервис уже кэширует
	// готовый JSON (поллер сериализует раз в 5с); раньше здесь вызывался
	// GetOrFetchSnapshot + json.Marshal на каждое WS-подключение.
	snapshotJSON, err := h.monitorService.GetOrFetchSnapshotJSON(c.Request.Context(), req.ID)
	if err != nil {
		log.Error().Err(err).Uint("game_id", req.ID).Msg("MonitorWS: failed to get snapshot")
	} else {
		// Неблокирующая отправка: если буфер клиента переполнен (write pump ещё не
		// стартовал или клиент медленный) — дропаем снапшот, не блокируя хендлер (M4).
		select {
		case client.Send <- snapshotJSON:
		default:
			log.Warn().Str("game_id", gameID).Msg("MonitorWS: client buffer full, dropping snapshot")
		}
	}

	// WebSocket lives beyond the HTTP handler — use a background context that is
	// canceled only when the connection goroutine finishes, NOT when the handler returns.
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
	// S-46-2 (pass 46): проверка доступа к комнате вынесена в canJoinRoom —
	// единая логика для ChatWS и других точек входа (персональная/командная/
	// капитанская/общая комнаты), чтобы правила не расходились и были тестируемы.
	ok, canErr := canJoinRoom(chatRoom, userID, chatAccessDeps{
		IsTeamMemberOrCaptain: h.chatService.IsTeamMemberOrCaptain,
		GetPassingByUser:      h.gameService.GetPassingByUser,
		IsTeamCaptain:         h.gameService.IsTeamCaptain,
		IsUserManager:         h.coAuthorSvc.IsUserManager,
	})
	if canErr != nil {
		log.Error().Err(canErr).Uint("room_id", uint(roomIDUint)).Uint("user_id", userID).Msg("ChatWS: access check error")
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "внутренняя ошибка"})
		return
	}
	if !ok {
		log.Warn().Uint("user_id", userID).Uint("room_id", uint(roomIDUint)).Msg("ChatWS: access denied")
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": render.Tr(c, "handler.forbidden")})
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Error().Err(err).Str("room_id", roomID).Msg("ChatWS: failed to upgrade connection")
		return
	}
	// После успешного апгрейда запрещаем дальнейшую запись в ответ
	c.Abort()

	// IDEA-6: помечаем комнату как чат (presence-рассылка) ДО регистрации —
	// иначе колбэк хаба не знает, что это чат, и пропустит presence.
	h.markChatRoom(roomID)
	client := ws.NewClientWithUser(conn, roomID, remoteIP, userID)
	h.hub.RegisterClient(client)
	defer func() {
		h.hub.UnregisterClient(client)
		client.Close()
		h.unmarkChatRoom(roomID)
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

	// S-46-3 (pass 46): read loop теперь прерывается по wsCtx.Done()/client.Done().
	// Раньше select с default никогда не блокировался — ctx.Done() наблюдался только
	// между блокирующими conn.ReadMessage() (до 60с), из-за чего при silent disconnect
	// или ошибке writePump goroutine висела до таймаута чтения (утечка соединения).
	// Чтение вынесено в goroutine: она блокируется на ReadMessage и шлёт результат
	// в канал; основной цикл select'ит между каналом, ctx.Done() и client.Done().
	type wsReadMsg struct {
		message []byte
		err     error
	}
	readCh := make(chan wsReadMsg, 1)
	go func() {
		for {
			if err := conn.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
				log.Debug().Err(err).Str("room_id", roomID).Msg("ChatWS: set read deadline failed")
			}
			_, message, err := conn.ReadMessage()
			select {
			case readCh <- wsReadMsg{message: message, err: err}:
			case <-wsCtx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-wsCtx.Done():
			log.Debug().Str("room_id", roomID).Msg("ChatWS: context canceled, stopping read loop")
			return
		case <-client.Done():
			log.Debug().Str("room_id", roomID).Msg("ChatWS: client closed, stopping read loop")
			return
		case rmsg := <-readCh:
			if rmsg.err != nil {
				if websocket.IsUnexpectedCloseError(rmsg.err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Debug().Err(rmsg.err).Str("room_id", roomID).Msg("ChatWS: read error")
				}
				return
			}
			// Client sends JSON: {"type":"message","room_id":...,"content":"..."}
			// или {"type":"load_older","before_id":N} (IDEA-11: ленивая история).
			var wsIn struct {
				Type     string `json:"type"`
				Content  string `json:"content"`
				BeforeID uint   `json:"before_id"`
			}
			if parseErr := json.Unmarshal(rmsg.message, &wsIn); parseErr != nil {
				// Legacy: сырой текст без JSON — считаем содержанием сообщения.
				wsIn.Type = "message"
				wsIn.Content = string(rmsg.message)
			}

			// IDEA-11: клиент просит старые сообщения (прокрутка вверх).
			if wsIn.Type == "load_older" {
				older, olderErr := h.chatService.GetMessagesBefore(wsCtx, uint(roomIDUint), wsIn.BeforeID, 50)
				if olderErr != nil {
					log.Error().Err(olderErr).Str("room_id", roomID).Msg("ChatWS: load_older failed")
					continue
				}
				payload, marshalErr := json.Marshal(gin.H{"type": "history_older", "messages": older})
				if marshalErr == nil {
					select {
					case client.Send <- payload:
					default:
						log.Warn().Str("room_id", roomID).Msg("ChatWS: client buffer full, dropping older history")
					}
				}
				continue
			}

			msgData := wsIn
			if msgData.Content == "" {
				continue
			}
			cleanContent := sanitize.StripHTML(msgData.Content)
			if cleanContent == "" {
				continue
			}
			if !msgLimiter.Allow() {
				log.Warn().Str("room_id", roomID).Uint("user_id", userID).Msg("ChatWS: message rate limit exceeded")
				continue
			}
			// S-46-5 (pass 46): единая проверка права на отправку (hot-path) —
			// recheck членства в команде + can_write комнаты одним вызовом.
			// Сохраняем прежнюю семантику:
			//  - участник, удалённый из команды → закрываем сокет;
			//  - запись члена комнаты с can_write=false → пропускаем сообщение.
			allowed, memberExists, permErr := h.chatService.CanSendMessage(wsCtx, uint(roomIDUint), chatRoom.TeamID, userID)
			if permErr != nil {
				log.Error().Err(permErr).Str("room_id", roomID).Uint("user_id", userID).Msg("ChatWS: send permission check error")
				return
			}
			if !allowed && chatRoom.TeamID != nil && !memberExists {
				log.Warn().Uint("team_id", *chatRoom.TeamID).Uint("user_id", userID).Msg("ChatWS: member removed from team, closing socket")
				return
			}
			if !allowed {
				log.Warn().Uint("room_id", uint(roomIDUint)).Uint("user_id", userID).Msg("ChatWS: write denied by room permissions")
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

// chatAccessDeps — зависимости для canJoinRoom (интерфейсы, чтобы можно было
// unit-тестировать правила доступа без полного графа сервисов).
type chatAccessDeps struct {
	IsTeamMemberOrCaptain func(ctx context.Context, teamID, userID uint) (bool, error)
	GetPassingByUser      func(ctx context.Context, gameID, userID uint) (*game.GamePassing, error)
	IsTeamCaptain         func(ctx context.Context, teamID, userID uint) (bool, error)
	IsUserManager         func(ctx context.Context, gameID, userID uint) (bool, error)
}

// canJoinRoom проверяет право пользователя подключиться к комнате чата
// (S-46-2, pass 46). Единая логика для ChatWS и других точек входа:
//   - personal: только два участника (User1ID/User2ID);
//   - командная (TeamID != nil): участник или капитан;
//   - «только капитаны»: капитан команды, участвующей в игре;
//   - остальные игровые комнаты: менеджер игры или участник прохождения;
//   - серверная/глобальная: любой аутентифицированный.
func canJoinRoom(room *ChatRoom, userID uint, deps chatAccessDeps) (bool, error) {
	// B-7 (pass 45): личная комната — доступ только двум участникам.
	if room.RoomType == RoomTypePersonal {
		if room.User1ID == nil || room.User2ID == nil ||
			(*room.User1ID != userID && *room.User2ID != userID) {
			return false, nil
		}
		return true, nil
	}

	// S-43-1 (pass 43): командные комнаты создаются со ВСЕМИ тремя полями
	// (GameID+TeamID+PassingID), поэтому проверка GameID-first ловила бы и командные
	// комнаты. Сначала проверяем членство в команде.
	if room.TeamID != nil {
		ok, memberErr := deps.IsTeamMemberOrCaptain(context.Background(), *room.TeamID, userID)
		if memberErr != nil {
			return false, memberErr
		}
		return ok, nil
	}

	if room.GameID == nil {
		// Серверная/глобальная комната — любой аутентифицированный.
		return true, nil
	}

	// B-2 (pass 45): комната «только капитаны» — доступ только капитанам команд.
	if room.RoomType == RoomTypeGameCaptains {
		passing, findErr := deps.GetPassingByUser(context.Background(), *room.GameID, userID)
		if findErr != nil {
			return false, nil
		}
		return deps.IsTeamCaptain(context.Background(), passing.TeamID, userID)
	}

	// Общая комната игры: менеджер или участник прохождения.
	isManager, mgrErr := deps.IsUserManager(context.Background(), *room.GameID, userID)
	if mgrErr != nil {
		return false, mgrErr
	}
	if isManager {
		return true, nil
	}
	if _, findErr := deps.GetPassingByUser(context.Background(), *room.GameID, userID); findErr != nil {
		return false, nil
	}
	return true, nil
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

	// B-2 (pass 45): комната «только капитаны» — создаётся для игры.
	captainsRoom, err := h.chatService.GetOrCreateCaptainsRoom(ctx, gameID)
	if err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("ChatRoomIDs: failed to get captains room")
		appErr := apperrors.Wrap(err, "MonitorHandler")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	var teamRoom *ChatRoom
	var floodRoom *ChatRoom
	passing, findErr := h.gameService.GetPassingByUser(ctx, gameID, userID)
	if findErr == nil {
		room, roomErr := h.chatService.GetOrCreateTeamRoom(ctx, gameID, passing.TeamID, passing.ID)
		if roomErr != nil {
			log.Error().Err(roomErr).Uint("game_id", gameID).Uint("team_id", passing.TeamID).Msg("ChatRoomIDs: failed to get team room")
		} else {
			teamRoom = room
		}
		// B-3 (pass 45): флудилка команды.
		froom, fErr := h.chatService.GetOrCreateTeamFloodRoom(ctx, gameID, passing.TeamID, passing.ID)
		if fErr != nil {
			log.Error().Err(fErr).Uint("game_id", gameID).Uint("team_id", passing.TeamID).Msg("ChatRoomIDs: failed to get team flood room")
		} else {
			floodRoom = froom
		}
	} else if !errors.Is(findErr, gorm.ErrRecordNotFound) {
		log.Error().Err(findErr).Uint("game_id", gameID).Uint("user_id", userID).Msg("ChatRoomIDs: failed to find passing")
	}

	resp := gin.H{
		"general_room_id":  generalRoom.ID,
		"captains_room_id": captainsRoom.ID,
	}
	if teamRoom != nil {
		resp["team_room_id"] = teamRoom.ID
	} else {
		resp["team_room_id"] = 0
	}
	if floodRoom != nil {
		resp["flood_room_id"] = floodRoom.ID
	} else {
		resp["flood_room_id"] = 0
	}

	c.JSON(http.StatusOK, resp)
}

// GlobalChatPage отображает общий чат всех игроков сервера (B-6, pass 45).
func (h *MonitorHandler) GlobalChatPage(c *gin.Context) {
	userID := c.GetUint("userID")
	room, err := h.chatService.GetOrCreateServerRoom(c.Request.Context())
	if err != nil {
		log.Error().Err(err).Msg("GlobalChatPage: failed to get server room")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	render.Page(c, http.StatusOK, "chat-global.html", gin.H{
		"Title":         "Общий чат",
		"RoomID":        room.ID,
		"CurrentUserID": userID,
		"csrf":          csrf.GetToken(c),
	})
}

// GlobalChatRoomID возвращает ID серверной комнаты (для WebSocket) (B-6).
func (h *MonitorHandler) GlobalChatRoomID(c *gin.Context) {
	room, err := h.chatService.GetOrCreateServerRoom(c.Request.Context())
	if err != nil {
		log.Error().Err(err).Msg("GlobalChatRoomID: failed to get server room")
		appErr := apperrors.Wrap(err, "MonitorHandler")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{"error": appErr.Message, "code": appErr.Code})
		return
	}
	c.JSON(http.StatusOK, gin.H{"room_id": room.ID})
}

// GameRooms возвращает список комнат игры (B-4, pass 45).
// @Summary Комнаты чата игры
// @Tags monitor
// @Produce json
// @Param id path int true "ID игры"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{} "handler.invalid_game_id"
// @Failure 500 {object} map[string]interface{} "handler.internal_error"
// @Security JWT
// @Router /games/{id}/chat/rooms [get]
func (h *MonitorHandler) GameRooms(c *gin.Context) {
	var req GameIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		appErr := apperrors.BadRequest(render.Tr(c, "handler.invalid_game_id"))
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{"error": appErr.Message, "code": appErr.Code})
		return
	}
	rooms, err := h.chatService.ListRoomsByGame(c.Request.Context(), req.ID)
	if err != nil {
		appErr := apperrors.Wrap(err, "MonitorHandler")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{"error": appErr.Message, "code": appErr.Code})
		return
	}
	c.JSON(http.StatusOK, gin.H{"rooms": rooms})
}

// CreateRoom создаёт произвольную комнату игры (B-4, pass 45).
// @Summary Создание комнаты чата
// @Tags monitor
// @Produce json
// @Param id path int true "ID игры"
// @Param name formData string true "Название комнаты"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{} "handler.invalid_game_id"
// @Failure 403 {object} map[string]interface{} "handler.forbidden"
// @Failure 500 {object} map[string]interface{} "handler.internal_error"
// @Security JWT
// @Router /games/{id}/chat/rooms [post]
func (h *MonitorHandler) CreateRoom(c *gin.Context) {
	var req GameIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		appErr := apperrors.BadRequest(render.Tr(c, "handler.invalid_game_id"))
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{"error": appErr.Message, "code": appErr.Code})
		return
	}
	userID := c.GetUint("userID")

	// Права: только менеджер игры (автор/соавтор).
	isMgr, mErr := h.coAuthorSvc.IsUserManager(c.Request.Context(), req.ID, userID)
	if mErr != nil || !isMgr {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": render.Tr(c, "handler.forbidden"), "code": "forbidden"})
		return
	}

	name := strings.TrimSpace(c.PostForm("name"))
	if name == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "имя комнаты обязательно", "code": "invalid_name"})
		return
	}
	gameID := req.ID
	ownerID := userID
	room := &ChatRoom{
		GameID:   &gameID,
		Name:     name,
		RoomType: RoomTypeGameGeneral,
		OwnerID:  &ownerID,
	}
	if err := h.chatService.CreateRoom(c.Request.Context(), room); err != nil {
		appErr := apperrors.Wrap(err, "MonitorHandler")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{"error": appErr.Message, "code": appErr.Code})
		return
	}
	c.JSON(http.StatusOK, gin.H{"room_id": room.ID})
}

// PersonalChat открывает личный чат 1-на-1 (B-7, pass 45).
// @Summary Личный чат
// @Tags monitor
// @Produce html
// @Param user_id path int true "ID собеседника"
// @Success 200 {string} string "страница чата"
// @Failure 400 {object} map[string]interface{} "handler.invalid_user_id"
// @Failure 500 {object} map[string]interface{} "handler.internal_error"
// @Security JWT
// @Router /chat/personal/{user_id} [get]
func (h *MonitorHandler) PersonalChat(c *gin.Context) {
	userID := c.GetUint("userID")
	otherID, _ := strconv.Atoi(c.Param("user_id"))
	if otherID <= 0 || uint(otherID) == userID {
		render.RenderError(c, http.StatusBadRequest, "некорректный собеседник")
		return
	}
	room, err := h.chatService.GetOrCreatePersonalRoom(c.Request.Context(), userID, uint(otherID))
	if err != nil {
		log.Error().Err(err).Int("other_id", otherID).Uint("user_id", userID).Msg("PersonalChat: failed to get/create room")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	render.Page(c, http.StatusOK, "chat-global.html", gin.H{
		"Title":         "Личный чат",
		"RoomID":        room.ID,
		"CurrentUserID": userID,
		"csrf":          csrf.GetToken(c),
	})
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
		// HIGH-4: «не участник» — это 403 Forbidden, а не 400. Раньше 400
		// позволял перечислять session_id/team_id по разнице кодов.
		if errors.Is(err, ErrNotTeamMember) {
			appErr := apperrors.Forbidden(render.LocalizeError(c, err.Error()))
			c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{"error": appErr.Message, "code": appErr.Code})
			return
		}
		switch err.Error() {
		case ErrVotingClosed.Error(), ErrInvalidOption.Error(), ErrVoteAlreadyCast.Error():
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
// @Failure 400 {object} map[string]interface{} "handler.invalid_session_id"
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
