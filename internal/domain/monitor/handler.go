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
	"gengine-0/internal/pkg/render"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
	csrf "github.com/utrack/gin-csrf"
	"gorm.io/gorm"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		host := r.Host
		if strings.HasPrefix(origin, "http://"+host) || strings.HasPrefix(origin, "https://"+host) {
			return true
		}
		return false
	},
}

// ---------- Входные структуры для валидации ----------

// GameIDRequest используется для валидации ID игры в URL.
type GameIDRequest struct {
	ID uint `uri:"id" binding:"required,gt=0"`
}

// GameIDAndSessionIDRequest используется для валидации ID игры и сессии голосования.
type GameIDAndSessionIDRequest struct {
	ID        uint `uri:"id" binding:"required,gt=0"`
	SessionID uint `uri:"session_id" binding:"required,gt=0"`
}

// StartVotingInput используется для запуска голосования.
type StartVotingInput struct {
	PassingID uint `form:"passing_id" binding:"required,gt=0"`
	LevelID   uint `form:"level_id" binding:"required,gt=0"`
}

// VoteInput используется для голосования.
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
}

func NewMonitorHandler(
	db *gorm.DB,
	monitorSvc *game.MonitorService,
	voteSvc *BlackboxVoteService,
	chatSvc *ChatService,
	hub *ws.RoomHub,
) *MonitorHandler {
	return &MonitorHandler{
		db:                  db,
		monitorService:      monitorSvc,
		blackboxVoteService: voteSvc,
		chatService:         chatSvc,
		hub:                 hub,
	}
}

// MonitorPage отображает HTML-страницу мониторинга.
func (h *MonitorHandler) MonitorPage(c *gin.Context) {
	var req GameIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}
	render.Page(c, http.StatusOK, "monitor-page.html", gin.H{
		"GameID": req.ID,
		"csrf":   csrf.GetToken(c),
	})
}

// MonitorStreamSSE предоставляет Server-Sent Events для обновлений прогресса игры.
// Это более лёгкая альтернатива WebSocket для однонаправленного мониторинга.
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

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	ctx := c.Request.Context()

	for {
		select {
		case <-ctx.Done():
			log.Debug().Uint("game_id", gameID).Msg("SSE connection closed by client")
			return
		case <-ticker.C:
			if _, err := fmt.Fprintf(c.Writer, ": ping\n\n"); err != nil {
				log.Debug().Err(err).Uint("game_id", gameID).Msg("SSE ping write error")
				return
			}
			c.Writer.Flush()
		default:
			snapshot, err := h.monitorService.GetOrFetchSnapshot(gameID)
			if err != nil {
				log.Error().Err(err).Uint("game_id", gameID).Msg("SSE: failed to get snapshot")
				_, _ = fmt.Fprintf(c.Writer, "event: error\ndata: {\"error\": \"%s\"}\n\n", err.Error())
				c.Writer.Flush()
				time.Sleep(2 * time.Second)
				continue
			}

			data, err := json.Marshal(snapshot)
			if err != nil {
				log.Error().Err(err).Uint("game_id", gameID).Msg("SSE: failed to marshal snapshot")
				time.Sleep(2 * time.Second)
				continue
			}

			if _, err := fmt.Fprintf(c.Writer, "event: update\ndata: %s\n\n", data); err != nil {
				log.Debug().Err(err).Uint("game_id", gameID).Msg("SSE write error")
				return
			}
			c.Writer.Flush()
			time.Sleep(1 * time.Second)
		}
	}
}

// MonitorWS обрабатывает WebSocket-соединение для live-обновлений прогресса.
// Оставлен для обратной совместимости, но рекомендуется использовать SSE.
func (h *MonitorHandler) MonitorWS(c *gin.Context) {
	var req GameIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		log.Warn().Err(err).Msg("MonitorWS: invalid game ID")
		return
	}
	gameID := strconv.Itoa(int(req.ID))
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Error().Err(err).Str("game_id", gameID).Msg("MonitorWS: failed to upgrade connection")
		return
	}
	client := ws.NewClient(conn, gameID)
	h.hub.RegisterClient(client)

	snapshot, err := h.monitorService.GetOrFetchSnapshot(req.ID)
	if err != nil {
		log.Error().Err(err).Uint("game_id", req.ID).Msg("MonitorWS: failed to get snapshot")
	} else {
		if data, err := json.Marshal(snapshot); err == nil {
			client.Send <- data
		} else {
			log.Error().Err(err).Uint("game_id", req.ID).Msg("MonitorWS: failed to marshal snapshot")
		}
	}

	// Используем контекст запроса для отмены горутины при закрытии соединения
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	go func() {
		defer func() {
			h.hub.UnregisterClient(client)
			client.Close()
		}()
		// Запускаем обработку WebSocket с поддержкой отмены
		ws.HandleWebSocketWithContext(ctx, client)
	}()
}

// ChatPage отображает HTML-страницу чата игры.
func (h *MonitorHandler) ChatPage(c *gin.Context) {
	var req GameIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}
	gameID := req.ID
	userID := c.GetUint("userID")

	ctx := c.Request.Context()
	var currentUser user.User
	userName := "Вы"
	if err := h.db.WithContext(ctx).First(&currentUser, userID).Error; err != nil {
		log.Warn().Err(err).Uint("user_id", userID).Msg("ChatPage: failed to get user name")
	} else {
		userName = currentUser.Name
	}

	var passingID *uint
	var teamID *uint

	var passing game.GamePassing
	err := h.db.
		WithContext(ctx).
		Joins("JOIN team_members ON team_members.team_id = game_passings.team_id").
		Where("game_passings.game_id = ? AND game_passings.status IN (?,?) AND team_members.user_id = ?",
			gameID, game.StatusAccepted, game.StatusStarted, userID).
		First(&passing).Error
	if err == nil {
		pID := passing.ID
		tID := passing.TeamID
		passingID = &pID
		teamID = &tID
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.Error().Err(err).Uint("game_id", gameID).Uint("user_id", userID).Msg("ChatPage: failed to find passing")
	}

	render.Page(c, http.StatusOK, "chat-page.html", gin.H{
		"GameID":    gameID,
		"PassingID": passingID,
		"TeamID":    teamID,
		"UserID":    userID,
		"UserName":  userName,
		"csrf":      csrf.GetToken(c),
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

	client := ws.NewClient(conn, roomID)
	h.hub.RegisterClient(client)

	msgs, err := h.chatService.GetMessages(c.Request.Context(), uint(roomIDUint), 50)
	if err != nil {
		log.Error().Err(err).Int("room_id", roomIDUint).Msg("ChatWS: failed to get history")
	} else {
		data, err := json.Marshal(gin.H{"type": "history", "messages": msgs})
		if err == nil {
			client.Send <- data
		} else {
			log.Error().Err(err).Int("room_id", roomIDUint).Msg("ChatWS: failed to marshal history")
		}
	}

	// Создаём контекст с отменой для управления горутинами
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	go func() {
		defer func() {
			h.hub.UnregisterClient(client)
			client.Close()
		}()

		// Запускаем писатель с поддержкой контекста
		ws.WritePumpWithContext(ctx, client)

		// Цикл чтения с поддержкой отмены
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
				msg, err := h.chatService.SaveMessage(c.Request.Context(), uint(roomIDUint), userID, string(message))
				if err != nil {
					log.Error().Err(err).Str("room_id", roomID).Uint("user_id", userID).Msg("ChatWS: failed to save message")
					continue
				}
				if err := h.db.Preload("User").First(&msg, msg.ID).Error; err != nil {
					log.Error().Err(err).Uint("msg_id", msg.ID).Msg("ChatWS: failed to preload user")
				}
				resp, err := json.Marshal(gin.H{"type": "message", "message": msg})
				if err != nil {
					log.Error().Err(err).Uint("msg_id", msg.ID).Msg("ChatWS: failed to marshal message")
					continue
				}
				h.hub.BroadcastToRoom(roomID, resp)
			}
		}
	}()
}

// ChatRoomIDs возвращает ID комнат чата (общая и командная) для игры.
func (h *MonitorHandler) ChatRoomIDs(c *gin.Context) {
	var req GameIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный ID игры"})
		return
	}
	gameID := req.ID
	userID := c.GetUint("userID")

	ctx := c.Request.Context()
	generalRoom, err := h.chatService.GetOrCreateGameRoom(ctx, gameID)
	if err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("ChatRoomIDs: failed to get general room")
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var teamRoom *ChatRoom
	var passing game.GamePassing
	err = h.db.
		WithContext(ctx).
		Joins("JOIN team_members ON team_members.team_id = game_passings.team_id").
		Where("game_passings.game_id = ? AND game_passings.status IN (?,?) AND team_members.user_id = ?",
			gameID, game.StatusAccepted, game.StatusStarted, userID).
		First(&passing).Error
	if err == nil {
		room, err := h.chatService.GetOrCreateTeamRoom(ctx, gameID, passing.TeamID, passing.ID)
		if err != nil {
			log.Error().Err(err).Uint("game_id", gameID).Uint("team_id", passing.TeamID).Msg("ChatRoomIDs: failed to get team room")
		} else {
			teamRoom = room
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.Error().Err(err).Uint("game_id", gameID).Uint("user_id", userID).Msg("ChatRoomIDs: failed to find passing")
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
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}
	gameID := req.ID

	var logs []game.Log
	if err := h.db.WithContext(c.Request.Context()).Where("game_id = ?", gameID).Order("created_at ASC").Find(&logs).Error; err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("ListLogs: failed to fetch logs")
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
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
	gameID := strconv.Itoa(int(req.ID))
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Error().Err(err).Str("game_id", gameID).Msg("LogsWS: failed to upgrade connection")
		return
	}
	client := ws.NewClient(conn, "logs_"+gameID)
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверные данные: " + err.Error()})
		return
	}

	userID := c.GetUint("userID")
	if err := h.blackboxVoteService.StartVoting(c.Request.Context(), input.PassingID, input.LevelID, userID); err != nil {
		log.Error().Err(err).Uint("passing_id", input.PassingID).Uint("level_id", input.LevelID).Uint("user_id", userID).Msg("StartVoting: failed to start voting")
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "Голосование запущено"})
}

// Vote обрабатывает голос команды за выбранный вариант.
func (h *MonitorHandler) Vote(c *gin.Context) {
	var input VoteInput
	if err := c.ShouldBind(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверные данные: " + err.Error()})
		return
	}

	if err := h.blackboxVoteService.Vote(c.Request.Context(), input.SessionID, input.TeamID, input.Option); err != nil {
		log.Error().Err(err).Uint("session_id", input.SessionID).Uint("team_id", input.TeamID).Str("option", input.Option).Msg("Vote: failed to vote")
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "Голос учтён"})
}

// GetVotingResults возвращает текущие результаты голосования.
func (h *MonitorHandler) GetVotingResults(c *gin.Context) {
	var req GameIDAndSessionIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный ID сессии"})
		return
	}
	results, err := h.blackboxVoteService.GetVotingResults(c.Request.Context(), req.SessionID)
	if err != nil {
		log.Error().Err(err).Uint("session_id", req.SessionID).Msg("GetVotingResults: failed to get results")
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"results": results})
}

// CloseVoting завершает голосование и определяет победителя.
func (h *MonitorHandler) CloseVoting(c *gin.Context) {
	var req GameIDAndSessionIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный ID сессии"})
		return
	}
	userID := c.GetUint("userID")

	winner, err := h.blackboxVoteService.CloseVoting(c.Request.Context(), req.SessionID, userID)
	if err != nil {
		log.Error().Err(err).Uint("session_id", req.SessionID).Uint("user_id", userID).Msg("CloseVoting: failed to close voting")
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"winner": winner})
}
