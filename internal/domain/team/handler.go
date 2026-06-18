// internal/domain/team/handler.go
package team

import (
	"net/http"
	"strconv"

	"gengine-0/internal/pkg/storage"

	"github.com/utrack/gin-csrf"
	"github.com/gin-gonic/gin"
)

// ---------- Входные структуры для валидации ----------

type CreateTeamInput struct {
	Name string `form:"name" binding:"required,min=2,max=100"`
}

type AddMemberInput struct {
	UserID uint `form:"user_id" binding:"required,gt=0"`
}

type ChangeCaptainInput struct {
	CaptainID uint `form:"captain_id" binding:"required,gt=0"`
}

type CreateInvitationInput struct {
	UserID uint `form:"user_id" binding:"required,gt=0"`
}

// ---------- Обработчики команд ----------

// TeamHandler обрабатывает запросы, связанные с командами.
type TeamHandler struct {
	teamService *TeamService
	storage     storage.FileStorage
}

func NewTeamHandler(teamService *TeamService, st storage.FileStorage) *TeamHandler {
	return &TeamHandler{
		teamService: teamService,
		storage:     st,
	}
}

// MyTeams отображает список команд текущего пользователя (капитанство + участие).
func (h *TeamHandler) MyTeams(c *gin.Context) {
	userID := c.GetUint("userID")
	teams, err := h.teamService.GetMyTeams(userID)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock":  "teams-my.html",
		"Teams":         teams,
		"CurrentUserID": userID, // для отображения навигации как у авторизованного
	})
}

// NewTeamForm показывает форму создания команды.
func (h *TeamHandler) NewTeamForm(c *gin.Context) {
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "teams-new.html",
		"csrf":         csrf.GetToken(c),
	})
}

// CreateTeam создаёт новую команду и делает текущего пользователя капитаном.
func (h *TeamHandler) CreateTeam(c *gin.Context) {
	var input CreateTeamInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "teams-new.html",
			"Error":        "Название должно быть от 2 до 100 символов",
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	userID := c.GetUint("userID")
	_, err := h.teamService.CreateTeam(input.Name, userID)
	if err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "teams-new.html",
			"Error":        err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	c.Redirect(http.StatusFound, "/teams")
}

// ViewTeam отображает состав команды вне контекста игры (по прямой ссылке /teams/:team_id).
func (h *TeamHandler) ViewTeam(c *gin.Context) {
	teamID, _ := strconv.Atoi(c.Param("team_id"))
	userID := c.GetUint("userID")

	team, members, err := h.teamService.GetTeamWithMembers(uint(teamID))
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}

	canManage := h.teamService.CanManageTeam(uint(teamID), userID)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock":  "teams-members.html",
		"Team":          team,
		"Members":       members,
		"CanManage":     canManage,
		"IsCaptain":     team.CaptainID == userID,
		"CurrentUserID": userID,
		// GameID не передаётся, поэтому кнопки управления (добавить, сменить) не покажутся
	})
}

// Members отображает состав конкретной команды в контексте игры.
func (h *TeamHandler) Members(c *gin.Context) {
	teamID, _ := strconv.Atoi(c.Param("team_id"))
	userID := c.GetUint("userID")

	team, members, err := h.teamService.GetTeamWithMembers(uint(teamID))
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}

	canManage := h.teamService.CanManageTeam(uint(teamID), userID)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock":  "teams-members.html",
		"GameID":        c.Param("game_id"),
		"TeamID":        teamID,
		"Team":          team,
		"Members":       members,
		"CanManage":     canManage,
		"IsCaptain":     team.CaptainID == userID,
		"CurrentUserID": userID,
	})
}

// AddMemberForm показывает форму добавления участника.
func (h *TeamHandler) AddMemberForm(c *gin.Context) {
	teamID, _ := strconv.Atoi(c.Param("team_id"))
	availableUsers, err := h.teamService.GetAvailableUsers(uint(teamID))
	if err != nil {
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "teams-add_member.html",
		"GameID":       c.Param("game_id"),
		"TeamID":       teamID,
		"Users":        availableUsers,
		"csrf":         csrf.GetToken(c),
	})
}

// AddMember добавляет нового участника.
func (h *TeamHandler) AddMember(c *gin.Context) {
	teamID, _ := strconv.Atoi(c.Param("team_id"))
	actorID := c.GetUint("userID")

	var input AddMemberInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "teams-add_member.html",
			"Error":        "Неверные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	if err := h.teamService.AddMember(uint(teamID), input.UserID, actorID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/teams/"+c.Param("team_id")+"/members")
}

// RemoveMember удаляет участника из команды.
func (h *TeamHandler) RemoveMember(c *gin.Context) {
	teamID, _ := strconv.Atoi(c.Param("team_id"))
	memberID, _ := strconv.Atoi(c.Param("member_id"))
	actorID := c.GetUint("userID")

	if err := h.teamService.RemoveMember(uint(teamID), uint(memberID), actorID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/teams/"+c.Param("team_id")+"/members")
}

// ChangeCaptainForm показывает форму смены капитана.
func (h *TeamHandler) ChangeCaptainForm(c *gin.Context) {
	teamID, _ := strconv.Atoi(c.Param("team_id"))
	_, members, err := h.teamService.GetTeamWithMembers(uint(teamID))
	if err != nil {
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "teams-change_captain.html",
		"GameID":       c.Param("game_id"),
		"TeamID":       teamID,
		"Members":      members,
		"csrf":         csrf.GetToken(c),
	})
}

// ChangeCaptain производит смену капитана.
func (h *TeamHandler) ChangeCaptain(c *gin.Context) {
	teamID, _ := strconv.Atoi(c.Param("team_id"))
	actorID := c.GetUint("userID")

	var input ChangeCaptainInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "teams-change_captain.html",
			"Error":        "Неверные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	if err := h.teamService.ChangeCaptain(uint(teamID), input.CaptainID, actorID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/teams/"+c.Param("team_id")+"/members")
}

// ---------- Приглашения ----------

// InvitationHandler обрабатывает приглашения.
type InvitationHandler struct {
	invitationService *InvitationService
}

func NewInvitationHandler(invitationService *InvitationService) *InvitationHandler {
	return &InvitationHandler{invitationService: invitationService}
}

// Index отображает список приглашений команды (для автора/капитана).
func (h *InvitationHandler) Index(c *gin.Context) {
	teamID, _ := strconv.Atoi(c.Param("team_id"))
	invitations, err := h.invitationService.ListByTeam(uint(teamID))
	if err != nil {
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "invitations-index.html",
		"GameID":       c.Param("game_id"),
		"TeamID":       teamID,
		"Invitations":  invitations,
	})
}

// NewForm показывает форму создания приглашения.
func (h *InvitationHandler) NewForm(c *gin.Context) {
	teamID, _ := strconv.Atoi(c.Param("team_id"))
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "invitations-new.html",
		"GameID":       c.Param("game_id"),
		"TeamID":       teamID,
		"csrf":         csrf.GetToken(c),
	})
}

// Create создаёт новое приглашение.
func (h *InvitationHandler) Create(c *gin.Context) {
	teamID, _ := strconv.Atoi(c.Param("team_id"))
	userID := c.GetUint("userID")

	var input CreateInvitationInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "invitations-new.html",
			"Error":        "Неверный ID пользователя",
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	_, err := h.invitationService.CreateInvitation(uint(teamID), input.UserID, userID)
	if err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "invitations-new.html",
			"Error":        err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/teams/"+c.Param("team_id")+"/invitations")
}

// MyInvitations отображает мои приглашения.
func (h *InvitationHandler) MyInvitations(c *gin.Context) {
	userID := c.GetUint("userID")
	invitations, err := h.invitationService.GetPendingForUser(userID)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock":  "invitations-my.html",
		"Invitations":   invitations,
		"CurrentUserID": userID,
	})
}

// Accept принимает приглашение.
func (h *InvitationHandler) Accept(c *gin.Context) {
	invitationID, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")
	if err := h.invitationService.AcceptInvitation(uint(invitationID), userID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/invitations/my")
}

// Decline отклоняет приглашение.
func (h *InvitationHandler) Decline(c *gin.Context) {
	invitationID, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")
	if err := h.invitationService.DeclineInvitation(uint(invitationID), userID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/invitations/my")
}