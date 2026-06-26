// internal/domain/team/handler.go
package team

import (
	"errors"
	"net/http"
	"strconv"

	"gengine-0/internal/pkg/storage"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	csrf "github.com/utrack/gin-csrf"
	"gorm.io/gorm"
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
	teams, err := h.teamService.GetMyTeams(c.Request.Context(), userID)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("MyTeams: failed to get teams")
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock":  "teams-my.html",
		"Teams":         teams,
		"CurrentUserID": userID,
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
		c.HTML(http.StatusBadRequest, "layout.html", gin.H{
			"ContentBlock": "teams-new.html",
			"Error":        "Название должно быть от 2 до 100 символов",
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	userID := c.GetUint("userID")
	_, err := h.teamService.CreateTeam(c.Request.Context(), input.Name, userID)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Str("name", input.Name).Msg("CreateTeam: failed to create team")
		c.HTML(http.StatusInternalServerError, "layout.html", gin.H{
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
	teamID, err := strconv.Atoi(c.Param("team_id"))
	if err != nil || teamID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID команды"})
		return
	}
	userID := c.GetUint("userID")

	team, members, err := h.teamService.GetTeamWithMembers(c.Request.Context(), uint(teamID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.HTML(http.StatusNotFound, "errors/404.html", nil)
		} else {
			log.Error().Err(err).Int("team_id", teamID).Msg("ViewTeam: failed to get team")
			c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		}
		return
	}

	canManage := h.teamService.CanManageTeam(c.Request.Context(), uint(teamID), userID)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock":  "teams-members.html",
		"Team":          team,
		"Members":       members,
		"CanManage":     canManage,
		"IsCaptain":     team.CaptainID == userID,
		"CurrentUserID": userID,
	})
}

// Members отображает состав конкретной команды в контексте игры.
func (h *TeamHandler) Members(c *gin.Context) {
	teamID, err := strconv.Atoi(c.Param("team_id"))
	if err != nil || teamID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID команды"})
		return
	}
	userID := c.GetUint("userID")

	team, members, err := h.teamService.GetTeamWithMembers(c.Request.Context(), uint(teamID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.HTML(http.StatusNotFound, "errors/404.html", nil)
		} else {
			log.Error().Err(err).Int("team_id", teamID).Msg("Members: failed to get team")
			c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		}
		return
	}

	canManage := h.teamService.CanManageTeam(c.Request.Context(), uint(teamID), userID)

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
	teamID, err := strconv.Atoi(c.Param("team_id"))
	if err != nil || teamID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID команды"})
		return
	}

	availableUsers, err := h.teamService.GetAvailableUsers(c.Request.Context(), uint(teamID))
	if err != nil {
		log.Error().Err(err).Int("team_id", teamID).Msg("AddMemberForm: failed to get available users")
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
	teamID, err := strconv.Atoi(c.Param("team_id"))
	if err != nil || teamID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID команды"})
		return
	}
	actorID := c.GetUint("userID")

	var input AddMemberInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusBadRequest, "layout.html", gin.H{
			"ContentBlock": "teams-add_member.html",
			"Error":        "Неверные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	if err := h.teamService.AddMember(c.Request.Context(), uint(teamID), input.UserID, actorID); err != nil {
		log.Error().Err(err).Int("team_id", teamID).Uint("user_id", input.UserID).Uint("actor_id", actorID).Msg("AddMember: failed to add member")
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/teams/"+c.Param("team_id")+"/members")
}

// RemoveMember удаляет участника из команды.
func (h *TeamHandler) RemoveMember(c *gin.Context) {
	teamID, err := strconv.Atoi(c.Param("team_id"))
	if err != nil || teamID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID команды"})
		return
	}
	memberID, err := strconv.Atoi(c.Param("member_id"))
	if err != nil || memberID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID участника"})
		return
	}
	actorID := c.GetUint("userID")

	if err := h.teamService.RemoveMember(c.Request.Context(), uint(teamID), uint(memberID), actorID); err != nil {
		log.Error().Err(err).Int("team_id", teamID).Int("member_id", memberID).Uint("actor_id", actorID).Msg("RemoveMember: failed to remove member")
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/teams/"+c.Param("team_id")+"/members")
}

// ChangeCaptainForm показывает форму смены капитана.
func (h *TeamHandler) ChangeCaptainForm(c *gin.Context) {
	teamID, err := strconv.Atoi(c.Param("team_id"))
	if err != nil || teamID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID команды"})
		return
	}

	_, members, err := h.teamService.GetTeamWithMembers(c.Request.Context(), uint(teamID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.HTML(http.StatusNotFound, "errors/404.html", nil)
		} else {
			log.Error().Err(err).Int("team_id", teamID).Msg("ChangeCaptainForm: failed to get team members")
			c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		}
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
	teamID, err := strconv.Atoi(c.Param("team_id"))
	if err != nil || teamID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID команды"})
		return
	}
	actorID := c.GetUint("userID")

	var input ChangeCaptainInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusBadRequest, "layout.html", gin.H{
			"ContentBlock": "teams-change_captain.html",
			"Error":        "Неверные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	if err := h.teamService.ChangeCaptain(c.Request.Context(), uint(teamID), input.CaptainID, actorID); err != nil {
		log.Error().Err(err).Int("team_id", teamID).Uint("captain_id", input.CaptainID).Uint("actor_id", actorID).Msg("ChangeCaptain: failed to change captain")
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
	teamID, err := strconv.Atoi(c.Param("team_id"))
	if err != nil || teamID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID команды"})
		return
	}

	invitations, err := h.invitationService.ListByTeam(c.Request.Context(), uint(teamID))
	if err != nil {
		log.Error().Err(err).Int("team_id", teamID).Msg("InvitationHandler.Index: failed to list invitations")
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
	teamID, err := strconv.Atoi(c.Param("team_id"))
	if err != nil || teamID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID команды"})
		return
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "invitations-new.html",
		"GameID":       c.Param("game_id"),
		"TeamID":       teamID,
		"csrf":         csrf.GetToken(c),
	})
}

// Create создаёт новое приглашение.
func (h *InvitationHandler) Create(c *gin.Context) {
	teamID, err := strconv.Atoi(c.Param("team_id"))
	if err != nil || teamID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID команды"})
		return
	}
	userID := c.GetUint("userID")

	var input CreateInvitationInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusBadRequest, "layout.html", gin.H{
			"ContentBlock": "invitations-new.html",
			"Error":        "Неверный ID пользователя",
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	_, err = h.invitationService.CreateInvitation(c.Request.Context(), uint(teamID), input.UserID, userID)
	if err != nil {
		log.Error().Err(err).Int("team_id", teamID).Uint("invited_user", input.UserID).Uint("inviter", userID).Msg("InvitationHandler.Create: failed to create invitation")
		c.HTML(http.StatusInternalServerError, "layout.html", gin.H{
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
	invitations, err := h.invitationService.GetPendingForUser(c.Request.Context(), userID)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("MyInvitations: failed to get pending invitations")
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
	invitationID, err := strconv.Atoi(c.Param("id"))
	if err != nil || invitationID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID приглашения"})
		return
	}
	userID := c.GetUint("userID")

	if err := h.invitationService.AcceptInvitation(c.Request.Context(), uint(invitationID), userID); err != nil {
		log.Error().Err(err).Int("invitation_id", invitationID).Uint("user_id", userID).Msg("Accept: failed to accept invitation")
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/invitations/my")
}

// Decline отклоняет приглашение.
func (h *InvitationHandler) Decline(c *gin.Context) {
	invitationID, err := strconv.Atoi(c.Param("id"))
	if err != nil || invitationID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID приглашения"})
		return
	}
	userID := c.GetUint("userID")

	if err := h.invitationService.DeclineInvitation(c.Request.Context(), uint(invitationID), userID); err != nil {
		log.Error().Err(err).Int("invitation_id", invitationID).Uint("user_id", userID).Msg("Decline: failed to decline invitation")
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/invitations/my")
}
