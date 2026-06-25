// internal/domain/monitor/handler.go
package monitor

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/user"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
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
// @Summary Страница мониторинга игры
// @Description Отображает страницу с live-обновлениями прогресса игры
// @Tags monitor
// @Produce html
// @Param id path int true "ID игры"
// @Success 200 {string} html "Страница мониторинга"
// @Router /games/{id}/monitor [get]
// @Security JWT
func (h *MonitorHandler) MonitorPage(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "monitor-page.html",
		"GameID":       gameID,
		"csrf":         csrf.GetToken(c),
	})
}

// MonitorWS обрабатывает WebSocket-соединение для live-обновлений прогресса.
// @Summary WebSocket мониторинга
// @Description Устанавливает WebSocket-соединение для получения обновлений прогресса игры
// @Tags monitor
// @Param id path int true "ID игры"
// @Success 101 {string} string "Switching Protocols"
// @Router /games/{id}/monitor/ws [get]
// @Security JWT
func (h *MonitorHandler) MonitorWS(c *gin.Context) {
	gameID := c.Param("id")
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	client := &ws.Client{
		Conn:   conn,
		Send:   make(chan []byte, 256),
		RoomID: gameID,
	}
	h.hub.RegisterClient(gameID, client)

	id, _ := strconv.Atoi(gameID)
	snapshot, err := h.monitorService.GetOrFetchSnapshot(uint(id))
	if err == nil {
		if data, err := json.Marshal(snapshot); err == nil {
			client.Send <- data
		}
	}

	go func() {
		defer func() {
			h.hub.UnregisterClient(client)
			client.Close()
		}()
		ws.HandleWebSocket(client)
	}()
}

// ChatPage отображает HTML-страницу чата игры.
// @Summary Страница чата игры
// @Description Отображает страницу чата для игры (общий и командный)
// @Tags monitor
// @Produce html
// @Param id path int true "ID игры"
// @Success 200 {string} html "Страница чата"
// @Router /games/{id}/chat [get]
// @Security JWT
func (h *MonitorHandler) ChatPage(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	ctx := c.Request.Context()
	var currentUser user.User
	userName := "Вы"
	if err := h.db.WithContext(ctx).First(&currentUser, userID).Error; err == nil {
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
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "chat-page.html",
		"GameID":       gameID,
		"PassingID":    passingID,
		"TeamID":       teamID,
		"UserID":       userID,
		"UserName":     userName,
		"csrf":         csrf.GetToken(c),
	})
}

// ChatWS обрабатывает WebSocket-соединение чата.
// @Summary WebSocket чата
// @Description Устанавливает WebSocket-соединение для обмена сообщениями в чате
// @Tags monitor
// @Param room query string true "ID комнаты чата"
// @Success 101 {string} string "Switching Protocols"
// @Router /chat/ws [get]
// @Security JWT
func (h *MonitorHandler) ChatWS(c *gin.Context) {
	roomID := c.Query("room")
	userID := c.GetUint("userID")

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}

	client := &ws.Client{
		Conn:   conn,
		Send:   make(chan []byte, 256),
		RoomID: roomID,
	}
	h.hub.RegisterClient(roomID, client)

	roomIDUint, _ := strconv.Atoi(roomID)
	msgs, err := h.chatService.GetMessages(c.Request.Context(), uint(roomIDUint), 50)
	if err == nil {
		data, _ := json.Marshal(gin.H{"type": "history", "messages": msgs})
		client.Send <- data
	}

	go func() {
		defer h.hub.UnregisterClient(client)
		ws.WritePump(client)
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				break
			}
			msg, err := h.chatService.SaveMessage(c.Request.Context(), uint(roomIDUint), userID, string(message))
			if err == nil {
				h.db.Preload("User").First(&msg, msg.ID)
				resp, _ := json.Marshal(gin.H{"type": "message", "message": msg})
				h.hub.BroadcastToRoom(roomID, resp)
			}
		}
	}()
}

// ChatRoomIDs возвращает ID комнат чата (общая и командная) для игры.
// @Summary ID комнат чата
// @Description Возвращает ID общей и командной комнат чата для игры
// @Tags monitor
// @Produce json
// @Param id path int true "ID игры"
// @Success 200 {object} map[string]interface{} "ID комнат чата"
// @Router /games/{id}/chat-rooms [get]
// @Security JWT
func (h *MonitorHandler) ChatRoomIDs(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	ctx := c.Request.Context()
	generalRoom, err := h.chatService.GetOrCreateGameRoom(ctx, uint(gameID))
	if err != nil {
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
		room, err := h.chatService.GetOrCreateTeamRoom(ctx, uint(gameID), passing.TeamID, passing.ID)
		if err == nil {
			teamRoom = room
		}
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
// @Description Отображает страницу с историей событий игры
// @Tags monitor
// @Produce html
// @Param id path int true "ID игры"
// @Success 200 {string} html "Страница логов"
// @Router /games/{id}/logs [get]
// @Security JWT
func (h *MonitorHandler) ListLogs(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))
	var logs []game.Log
	h.db.WithContext(c.Request.Context()).Where("game_id = ?", gameID).Order("created_at ASC").Find(&logs)
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "logs-list.html",
		"GameID":       gameID,
		"Logs":         logs,
		"csrf":         csrf.GetToken(c),
	})
}

// LogsWS предоставляет WebSocket-стрим логов игры.
// @Summary WebSocket логов
// @Description Устанавливает WebSocket-соединение для потоковой передачи логов игры
// @Tags monitor
// @Param id path int true "ID игры"
// @Success 101 {string} string "Switching Protocols"
// @Router /games/{id}/logs/ws [get]
// @Security JWT
func (h *MonitorHandler) LogsWS(c *gin.Context) {
	gameID := c.Param("id")
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	client := &ws.Client{
		Conn:   conn,
		Send:   make(chan []byte, 256),
		RoomID: "logs_" + gameID,
	}
	h.hub.RegisterClient("logs_"+gameID, client)
	go func() {
		defer func() {
			h.hub.UnregisterClient(client)
			client.Close()
		}()
		ws.HandleWebSocket(client)
	}()
}

// StartVoting запускает голосование по текущему уровню-чёрному ящику.
// @Summary Запуск голосования
// @Description Запускает голосование на уровне-чёрном ящике (доступно автору игры)
// @Tags monitor
// @Accept x-www-form-urlencoded
// @Produce json
// @Param passing_id formData uint true "ID прохождения"
// @Param level_id formData uint true "ID уровня"
// @Success 200 {object} map[string]interface{} "Голосование запущено"
// @Failure 400 {object} map[string]interface{} "Ошибка"
// @Router /voting/start [post]
// @Security JWT
func (h *MonitorHandler) StartVoting(c *gin.Context) {
	var input StartVotingInput
	if err := c.ShouldBind(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверные данные: " + err.Error()})
		return
	}

	userID := c.GetUint("userID")
	if err := h.blackboxVoteService.StartVoting(c.Request.Context(), input.PassingID, input.LevelID, userID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "Голосование запущено"})
}

// Vote обрабатывает голос команды за выбранный вариант.
// @Summary Голосование
// @Description Команда голосует за вариант ответа на уровне-чёрном ящике
// @Tags monitor
// @Accept x-www-form-urlencoded
// @Produce json
// @Param session_id formData uint true "ID сессии голосования"
// @Param team_id formData uint true "ID команды"
// @Param option formData string true "Выбранный вариант"
// @Success 200 {object} map[string]interface{} "Голос учтён"
// @Failure 400 {object} map[string]interface{} "Ошибка"
// @Router /voting/vote [post]
// @Security JWT
func (h *MonitorHandler) Vote(c *gin.Context) {
	var input VoteInput
	if err := c.ShouldBind(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверные данные: " + err.Error()})
		return
	}

	if err := h.blackboxVoteService.Vote(c.Request.Context(), input.SessionID, input.TeamID, input.Option); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "Голос учтён"})
}

// GetVotingResults возвращает текущие результаты голосования.
// @Summary Результаты голосования
// @Description Возвращает текущие результаты голосования по сессии
// @Tags monitor
// @Produce json
// @Param session_id path int true "ID сессии голосования"
// @Success 200 {object} map[string]interface{} "Результаты голосования"
// @Failure 500 {object} map[string]interface{} "Ошибка"
// @Router /voting/{session_id}/results [get]
// @Security JWT
func (h *MonitorHandler) GetVotingResults(c *gin.Context) {
	sessionID, _ := strconv.Atoi(c.Param("session_id"))
	results, err := h.blackboxVoteService.GetVotingResults(c.Request.Context(), uint(sessionID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"results": results})
}

// CloseVoting завершает голосование и определяет победителя.
// @Summary Закрытие голосования
// @Description Завершает голосование и определяет победителя (доступно автору игры)
// @Tags monitor
// @Accept x-www-form-urlencoded
// @Produce json
// @Param session_id path int true "ID сессии голосования"
// @Success 200 {object} map[string]interface{} "Победивший вариант"
// @Failure 400 {object} map[string]interface{} "Ошибка"
// @Router /voting/{session_id}/close [post]
// @Security JWT
func (h *MonitorHandler) CloseVoting(c *gin.Context) {
	sessionID, _ := strconv.Atoi(c.Param("session_id"))
	userID := c.GetUint("userID")

	winner, err := h.blackboxVoteService.CloseVoting(c.Request.Context(), uint(sessionID), userID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"winner": winner})
}
