// internal/domain/team/chat_handler.go
package team

import (
	"net/http"
	"strconv"

	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"
	ws "gengine-0/internal/pkg/websocket"

	csrf "gengine-0/internal/pkg/csrf"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// teamChatRoom — локальная модель chat_rooms для избежания циклического импорта с monitor.
type teamChatRoom struct {
	ID     uint
	TeamID *uint
	Name   string
}

func (teamChatRoom) TableName() string { return "chat_rooms" }

// ChatHandler обрабатывает чат команды.
type ChatHandler struct {
	db      *gorm.DB
	hub     *ws.RoomHub
	teamSvc *TeamService
}

// NewChatHandler создаёт обработчик чата команды.
func NewChatHandler(db *gorm.DB, hub *ws.RoomHub, teamSvc *TeamService) *ChatHandler {
	return &ChatHandler{db: db, hub: hub, teamSvc: teamSvc}
}

// TeamChatPage отображает страницу чата команды.
func (h *ChatHandler) TeamChatPage(c *gin.Context) {
	teamID, err := strconv.Atoi(c.Param("team_id"))
	if err != nil || teamID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID команды")
		return
	}
	userID := c.GetUint("userID")

	// Проверяем, что пользователь — участник команды
	team, _, getErr := h.teamSvc.GetTeamWithMembers(c.Request.Context(), uint(teamID))
	if getErr != nil {
		render.RenderErrorPage(c, http.StatusNotFound)
		return
	}

	isMember, _ := h.teamSvc.teamRepo.IsMember(c.Request.Context(), uint(teamID), userID)
	if !isMember && team.CaptainID != userID && !middleware.IsAdmin(c) {
		render.RenderErrorPage(c, http.StatusForbidden)
		return
	}

	// Находим или создаём комнату чата для команды
	chatRoom, roomErr := h.findOrCreateTeamRoom(c, uint(teamID), team.Name)
	if roomErr != nil {
		log.Error().Err(roomErr).Uint("team_id", uint(teamID)).Msg("TeamChat: failed to get/create room")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	userName := "Вы"
	if u, _ := h.teamSvc.teamRepo.GetUserByID(c.Request.Context(), userID); u != nil {
		userName = u.Email
	}

	isAdmin := middleware.IsAdmin(c)

	render.Page(c, http.StatusOK, "team-chat.html", gin.H{
		"Title":         "Чат команды " + team.Name,
		"Team":          team,
		"RoomID":        chatRoom.ID,
		"UserID":        userID,
		"UserName":      userName,
		"csrf":          csrf.GetToken(c),
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
		"Breadcrumbs": []map[string]string{
			{"name": "Главная", "url": "/"},
			{"name": "Команды", "url": "/teams"},
			{"name": team.Name, "url": "/teams/" + strconv.Itoa(int(team.ID))},
			{"name": "Чат"},
		},
	})
}

// findOrCreateTeamRoom находит или создаёт комнату чата для команды.
func (h *ChatHandler) findOrCreateTeamRoom(c *gin.Context, teamID uint, teamName string) (*teamChatRoom, error) {
	var room teamChatRoom
	err := h.db.WithContext(c.Request.Context()).Where("team_id = ? AND game_id IS NULL", teamID).First(&room).Error
	if err == nil {
		return &room, nil
	}

	room = teamChatRoom{
		TeamID: &teamID,
		Name:   "Команда: " + teamName,
	}
	if createErr := h.db.WithContext(c.Request.Context()).Create(&room).Error; createErr != nil {
		return nil, createErr
	}
	return &room, nil
}
