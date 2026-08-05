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
	hub     *ws.RoomHub
	teamSvc *TeamService
}

// NewChatHandler создаёт обработчик чата команды.
func NewChatHandler(hub *ws.RoomHub, teamSvc *TeamService) *ChatHandler {
	return &ChatHandler{hub: hub, teamSvc: teamSvc}
}

// TeamChatPage отображает страницу чата команды.
func (h *ChatHandler) TeamChatPage(c *gin.Context) {
	teamID, err := strconv.Atoi(c.Param("team_id"))
	if err != nil || teamID <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_team_id"))
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
			{"name": "nav.home", "url": "/"},
			{"name": "nav.teams", "url": "/teams"},
			{"name": team.Name, "url": "/teams/" + strconv.Itoa(int(team.ID))},
			{"name": "nav.chat"},
		},
	})
}

// findOrCreateTeamRoom находит или создаёт комнату чата для команды (C1 — через репозиторий).
func (h *ChatHandler) findOrCreateTeamRoom(c *gin.Context, teamID uint, teamName string) (*teamChatRoom, error) {
	return h.teamSvc.GetOrCreateTeamChatRoom(c.Request.Context(), teamID, teamName)
}
