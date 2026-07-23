// internal/domain/monitor/handler.go
package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
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
			return true
		}
		host := r.Host
		if forwardedHost := r.Header.Get("X-Forwarded-Host"); forwardedHost != "" {
			host = forwardedHost
		}
		if strings.HasPrefix(origin, "http://"+host) || strings.HasPrefix(origin, "https://"+host) {
			return true
		}
		return false
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

type MonitorHandler struct {
	db                  *gorm.DB
	monitorService      *game.MonitorService
	blackboxVoteService *BlackboxVoteService
	chatService         *ChatService
	hub                 *ws.RoomHub
	userService         *user.UserService
	gameService         *game.GameService
	coAuthorSvc         *game.CoAuthorService
}

func NewMonitorHandler(
	db *gorm.DB,
	monitorSvc *game.MonitorService,
	voteSvc *BlackboxVoteService,
	chatSvc *ChatService,
	hub *ws.RoomHub,
	userSvc *user.UserService,
	gameSvc *game.GameService,
	coAuthorSvc *game.CoAuthorService,
) *MonitorHandler {
	return &MonitorHandler{
		db:                  db,
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
func (h *MonitorHandler) MonitorPage(c *gin.Context) {
	var req GameIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}

	userID := c.GetUint("userID")
	isAdmin := middleware.IsAdmin(c)

	render.Page(c, http.StatusOK, "monitor-page.html", gin.H{
		"GameID":        req.ID,
		"csrf":          csrf.GetToken(c),
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
	})
}

// MonitorData возвращает snapshot прогресса игры в JSON (для polling fallback).
func (h *MonitorHandler) MonitorData(c *gin.Context) {
	var req GameIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный ID игры"})
		return
	}

	snapshot, err := h.monitorService.GetOrFetchSnapshot(c.Request.Context(), req.ID)
	if err != nil {
		log.Error().Err(err).Uint("game_id", req.ID).Msg("MonitorWS: failed to get snapshot")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Не удалось получить данные мониторинга"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"teams": snapshot})
}

// MonitorStreamSSE предоставляет Server-Sent Events для обновлений прогресса игры.
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

	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()

	updateTicker := time.NewTicker(1 * time.Second)
	defer updateTicker.Stop()

	ctx := c.Request.Context()

	for {
		select {
		case <-ctx.Done():
			log.Debug().Uint("game_id", gameID).Msg("SSE connection closed by client")
			return
		case <-pingTicker.C:
			if _, err := fmt.Fprintf(c.Writer, ": ping\n\n"); err != nil {
				log.Debug().Err(err).Uint("game_id", gameID).Msg("SSE ping write error")
				return
			}
			c.Writer.Flush()
		case <-updateTicker.C:
			snapshot, err := h.monitorService.GetOrFetchSnapshot(c.Request.Context(), req.ID)
			if err != nil {
				log.Error().Err(err).Uint("game_id", req.ID).Msg("MonitorStreamSSE: failed to get snapshot")
				if _, writeErr := fmt.Fprintf(c.Writer, "event: error\ndata: {\"error\": \"%s\"}\n\n", err.Error()); writeErr != nil {
					log.Error().Err(writeErr).Uint("game_id", req.ID).Msg("MonitorStreamSSE: failed to write error event")
				}
				c.Writer.Flush()
				continue
			}

			data, marshalErr := json.Marshal(snapshot)
			if marshalErr != nil {
				log.Error().Err(marshalErr).Uint("game_id", gameID).Msg("SSE: failed to marshal snapshot")
				continue
			}

			if _, writeErr := fmt.Fprintf(c.Writer, "event: update\ndata: %s\n\n", data); writeErr != nil {
				log.Debug().Err(writeErr).Uint("game_id", gameID).Msg("SSE write error")
				return
			}
			c.Writer.Flush()
		}
	}
}

// MonitorWS обрабатывает WebSocket-соединение для live-обновлений прогресса.
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
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "требуется аутентификация"})
		return
	}

	// 🔒 Проверка прав доступа к игре (автор, соавтор или модератор)
	ok, err := h.coAuthorSvc.IsUserManager(c.Request.Context(), req.ID, userID)
	if err != nil || !ok {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "нет доступа к мониторингу"})
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

	snapshot, err := h.monitorService.GetOrFetchSnapshot(c.Request.Context(), req.ID)
	if err != nil {
		log.Error().Err(err).Uint("game_id", req.ID).Msg("MonitorWS: failed to get snapshot")
	} else {
		if data, err := json.Marshal(snapshot); err == nil {
			client.Send <- data
		} else {
			log.Error().Err(err).Uint("game_id", req.ID).Msg("MonitorWS: failed to marshal snapshot")
		}
	}

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	go func() {
		defer func() {
			h.hub.UnregisterClient(client)
			client.Close()
		}()
		ws.HandleWebSocketWithContext(ctx, client)
	}()
}

// ChatPage отображает HTML-страницу чата игры.// ChatPage отображает HTML-страницу чата игры.
func (h *MonitorHandler) ChatPage(c *gin.Context) {
	var req GameIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
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
		"GameID":        gameID,
		"PassingID":     passingID,
		"TeamID":        teamID,
		"UserID":        userID,
		"UserName":      userName,
		"csrf":          csrf.GetToken(c),
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
	})
}

// ChatWS обрабатывает WebSocket-соединение чата.
func (h *MonitorHandler) ChatWS(c *gin.Context) {
	roomID := c.Query("room")
	if roomID == "" {
		log.Warn().Msg("ChatWS: missing room parameter")
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	userID := c.GetUint("userID")
	if userID == 0 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "требуется аутентификация"})
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

	msgs, err := h.chatService.GetMessages(c.Request.Context(), uint(roomIDUint), 50)
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
			client.Send <- data
		} else {
			log.Error().Err(err).Int("room_id", roomIDUint).Msg("ChatWS: failed to marshal history")
		}
	}

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	// Используем экспортируемую функцию из пакета websocket
	go ws.WritePumpWithContext(ctx, client)

	for {
		select {
		case <-ctx.Done():
			log.Debug().Str("room_id", roomID).Msg("ChatWS: context cancelled, stopping read loop")
			return
		default:
			_, message, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Error().Err(err).Str("room_id", roomID).Msg("ChatWS: read error")
				}
				return
			}
			cleanContent := sanitize.StripHTML(string(message))
			if cleanContent == "" {
				continue
			}
			msg, err := h.chatService.SaveMessage(c.Request.Context(), uint(roomIDUint), userID, cleanContent)
			if err != nil {
				log.Error().Err(err).Str("room_id", roomID).Uint("user_id", userID).Msg("ChatWS: failed to save message")
				continue
			}
			if preloadErr := h.db.WithContext(c.Request.Context()).Preload("User").First(&msg, msg.ID).Error; preloadErr != nil {
				log.Error().Err(preloadErr).Uint("msg_id", msg.ID).Msg("ChatWS: failed to preload user")
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
func (h *MonitorHandler) ChatRoomIDs(c *gin.Context) {
	var req GameIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		appErr := apperrors.BadRequest("Неверный ID игры")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}
	gameID := req.ID
	userID := c.GetUint("userID")

	ctx := c.Request.Context()
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
	var passing game.GamePassing
	findErr := h.db.
		WithContext(ctx).
		Joins("JOIN team_members ON team_members.team_id = game_passings.team_id").
		Where("game_passings.game_id = ? AND game_passings.status IN (?,?) AND team_members.user_id = ?",
			gameID, game.StatusAccepted, game.StatusStarted, userID).
		First(&passing).Error
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
func (h *MonitorHandler) ListLogs(c *gin.Context) {
	var req GameIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	gameID := req.ID

	logs, err := h.gameService.GetLogsByGameID(c.Request.Context(), gameID)
	if err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("ListLogs: failed to fetch logs")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	render.Page(c, http.StatusOK, "logs-list.html", gin.H{
		"GameID": gameID,
		"Logs":   logs,
		"csrf":   csrf.GetToken(c),
	})
}

// LogsWS предоставляет WebSocket-стрим логов игры.
func (h *MonitorHandler) LogsWS(c *gin.Context) {
	var req GameIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		log.Warn().Err(err).Msg("LogsWS: invalid game ID")
		return
	}
	userID := c.GetUint("userID")
	if userID == 0 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "требуется аутентификация"})
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

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	go func() {
		defer func() {
			h.hub.UnregisterClient(client)
			client.Close()
		}()
		ws.HandleWebSocketWithContext(ctx, client)
	}()
}

// StartVoting запускает голосование по текущему уровню-чёрному ящику.
func (h *MonitorHandler) StartVoting(c *gin.Context) {
	var input StartVotingInput
	if err := c.ShouldBind(&input); err != nil {
		appErr := apperrors.BadRequest("Неверные данные: " + err.Error())
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	if err := validation.ValidatePositiveUint("ID прохождения", input.PassingID); err != nil {
		appErr := apperrors.BadRequest(err.Error())
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}
	if err := validation.ValidatePositiveUint("ID уровня", input.LevelID); err != nil {
		appErr := apperrors.BadRequest(err.Error())
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
			appErr := apperrors.BadRequest(err.Error())
			c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{"error": appErr.Message, "code": appErr.Code})
		default:
			log.Error().Err(err).Uint("passing_id", input.PassingID).Uint("level_id", input.LevelID).Uint("user_id", userID).Msg("StartVoting: failed to start voting")
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Внутренняя ошибка", "code": "INTERNAL_ERROR"})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "Голосование запущено"})
}

// Vote обрабатывает голос команды за выбранный вариант.
func (h *MonitorHandler) Vote(c *gin.Context) {
	var input VoteInput
	if err := c.ShouldBind(&input); err != nil {
		appErr := apperrors.BadRequest("Неверные данные: " + err.Error())
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	if err := validation.ValidatePositiveUint("ID сессии", input.SessionID); err != nil {
		appErr := apperrors.BadRequest(err.Error())
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}
	if err := validation.ValidatePositiveUint("ID команды", input.TeamID); err != nil {
		appErr := apperrors.BadRequest(err.Error())
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}
	if err := validation.ValidateString("Вариант ответа", input.Option, 1, 1000); err != nil {
		appErr := apperrors.BadRequest(err.Error())
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	cleanOption := sanitize.StripHTML(input.Option)

	if err := h.blackboxVoteService.Vote(c.Request.Context(), input.SessionID, input.TeamID, cleanOption); err != nil {
		switch err.Error() {
		case "голосование закрыто", "недопустимый вариант ответа", "ваш голос уже учтён":
			appErr := apperrors.BadRequest(err.Error())
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
func (h *MonitorHandler) GetVotingResults(c *gin.Context) {
	var req GameIDAndSessionIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		appErr := apperrors.BadRequest("Неверный ID сессии")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}
	results, err := h.blackboxVoteService.GetVotingResults(c.Request.Context(), req.SessionID)
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
func (h *MonitorHandler) CloseVoting(c *gin.Context) {
	var req GameIDAndSessionIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		appErr := apperrors.BadRequest("Неверный ID сессии")
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
		appErr := apperrors.Forbidden(err.Error())
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"winner": sanitize.StripHTML(winner)})
}
