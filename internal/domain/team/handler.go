// internal/domain/team/handler.go
package team

import (
	"errors"
	"net/http"
	"strconv"

	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/sanitize"
	"gengine-0/internal/pkg/storage"
	"gengine-0/internal/pkg/validation"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	csrf "github.com/utrack/gin-csrf"
	"gorm.io/gorm"
)

// ---------- Входные структуры для валидации ----------

// TeamIDRequest используется для валидации ID команды в URL.
type TeamIDRequest struct {
	TeamID uint `uri:"team_id" binding:"required,gt=0"`
}

// TeamIDAndMemberIDRequest используется для валидации ID команды и участника.
type TeamIDAndMemberIDRequest struct {
	TeamID   uint `uri:"team_id" binding:"required,gt=0"`
	MemberID uint `uri:"member_id" binding:"required,gt=0"`
}

// InvitationIDRequest используется для валидации ID приглашения.
type InvitationIDRequest struct {
	ID uint `uri:"id" binding:"required,gt=0"`
}

// CreateTeamInput используется для создания команды.
type CreateTeamInput struct {
	Name string `form:"name" binding:"required,min=2,max=100"`
}

// AddMemberInput используется для добавления участника.
type AddMemberInput struct {
	UserID uint `form:"user_id" binding:"required,gt=0"`
}

// ChangeCaptainInput используется для смены капитана.
type ChangeCaptainInput struct {
	CaptainID uint `form:"captain_id" binding:"required,gt=0"`
}

// CreateInvitationInput используется для создания приглашения.
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
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	isAdmin := middleware.IsAdmin(c)

	render.Page(c, http.StatusOK, "teams-my.html", gin.H{
		"Teams":         teams,
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
	})
}

// NewTeamForm показывает форму создания команды.
func (h *TeamHandler) NewTeamForm(c *gin.Context) {
	userID := c.GetUint("userID")
	isAdmin := middleware.IsAdmin(c)
	render.Page(c, http.StatusOK, "teams-new.html", gin.H{
		"csrf":          csrf.GetToken(c),
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
	})
}

// CreateTeam создаёт новую команду и делает текущего пользователя капитаном.
func (h *TeamHandler) CreateTeam(c *gin.Context) {
	var input CreateTeamInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "teams-new.html", gin.H{
			"Error": "Название должно быть от 2 до 100 символов",
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	// Санитизация названия команды
	cleanName := sanitize.StripHTML(input.Name)

	userID := c.GetUint("userID")
	_, err := h.teamService.CreateTeam(c.Request.Context(), cleanName, userID)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Str("name", cleanName).Msg("CreateTeam: failed to create team")
		render.Page(c, http.StatusInternalServerError, "teams-new.html", gin.H{
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	c.Redirect(http.StatusFound, "/teams")
}

// ViewTeam отображает состав команды вне контекста игры (по прямой ссылке /teams/:team_id).
func (h *TeamHandler) ViewTeam(c *gin.Context) {
	var req TeamIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID команды")
		return
	}
	userID := c.GetUint("userID")

	team, members, err := h.teamService.GetTeamWithMembers(c.Request.Context(), req.TeamID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			render.RenderErrorPage(c, http.StatusNotFound)
		} else {
			log.Error().Err(err).Uint("team_id", req.TeamID).Msg("ViewTeam: failed to get team")
			render.RenderErrorPage(c, http.StatusInternalServerError)
		}
		return
	}

	canManage := h.teamService.CanManageTeam(c.Request.Context(), req.TeamID, userID)

	isAdmin := middleware.IsAdmin(c)

	render.Page(c, http.StatusOK, "teams-members.html", gin.H{
		"Team":          team,
		"Members":       members,
		"CanManage":     canManage,
		"IsCaptain":     team.CaptainID == userID,
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
	})
}

// Members отображает состав конкретной команды в контексте игры.
func (h *TeamHandler) Members(c *gin.Context) {
	var req TeamIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID команды")
		return
	}
	userID := c.GetUint("userID")

	team, members, err := h.teamService.GetTeamWithMembers(c.Request.Context(), req.TeamID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			render.RenderErrorPage(c, http.StatusNotFound)
		} else {
			log.Error().Err(err).Uint("team_id", req.TeamID).Msg("Members: failed to get team")
			render.RenderErrorPage(c, http.StatusInternalServerError)
		}
		return
	}

	canManage := h.teamService.CanManageTeam(c.Request.Context(), req.TeamID, userID)

	isAdmin := middleware.IsAdmin(c)

	render.Page(c, http.StatusOK, "teams-members.html", gin.H{
		"GameID":        c.Param("game_id"),
		"TeamID":        req.TeamID,
		"Team":          team,
		"Members":       members,
		"CanManage":     canManage,
		"IsCaptain":     team.CaptainID == userID,
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
	})
}

// AddMemberForm показывает форму добавления участника.
func (h *TeamHandler) AddMemberForm(c *gin.Context) {
	var req TeamIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID команды")
		return
	}

	availableUsers, err := h.teamService.GetAvailableUsers(c.Request.Context(), req.TeamID)
	if err != nil {
		log.Error().Err(err).Uint("team_id", req.TeamID).Msg("AddMemberForm: failed to get available users")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	userID := c.GetUint("userID")
	isAdmin := middleware.IsAdmin(c)

	render.Page(c, http.StatusOK, "teams-add_member.html", gin.H{
		"GameID":        c.Param("game_id"),
		"TeamID":        req.TeamID,
		"Users":         availableUsers,
		"csrf":          csrf.GetToken(c),
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
	})
}

// AddMember добавляет нового участника.
func (h *TeamHandler) AddMember(c *gin.Context) {
	var req TeamIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID команды")
		return
	}
	actorID := c.GetUint("userID")

	var input AddMemberInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "teams-add_member.html", gin.H{
			"Error": "Неверные данные: " + err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	// Валидация ID пользователя
	if err := validation.ValidatePositiveUint("ID пользователя", input.UserID); err != nil {
		render.Page(c, http.StatusBadRequest, "teams-add_member.html", gin.H{
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	if err := h.teamService.AddMember(c.Request.Context(), req.TeamID, input.UserID, actorID); err != nil {
		log.Error().Err(err).Uint("team_id", req.TeamID).Uint("user_id", input.UserID).Uint("actor_id", actorID).Msg("AddMember: failed to add member")
		render.RenderError(c, http.StatusForbidden, err.Error())
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/teams/"+strconv.Itoa(int(req.TeamID))+"/members")
}

// RemoveMember удаляет участника из команды.
func (h *TeamHandler) RemoveMember(c *gin.Context) {
	var req TeamIDAndMemberIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверные ID")
		return
	}
	actorID := c.GetUint("userID")

	if err := h.teamService.RemoveMember(c.Request.Context(), req.TeamID, req.MemberID, actorID); err != nil {
		log.Error().Err(err).Uint("team_id", req.TeamID).Uint("member_id", req.MemberID).Uint("actor_id", actorID).Msg("RemoveMember: failed to remove member")
		render.RenderError(c, http.StatusForbidden, err.Error())
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/teams/"+strconv.Itoa(int(req.TeamID))+"/members")
}

// ChangeCaptainForm показывает форму смены капитана.
func (h *TeamHandler) ChangeCaptainForm(c *gin.Context) {
	var req TeamIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID команды")
		return
	}

	_, members, err := h.teamService.GetTeamWithMembers(c.Request.Context(), req.TeamID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			render.RenderErrorPage(c, http.StatusNotFound)
		} else {
			log.Error().Err(err).Uint("team_id", req.TeamID).Msg("ChangeCaptainForm: failed to get team members")
			render.RenderErrorPage(c, http.StatusInternalServerError)
		}
		return
	}

	userID := c.GetUint("userID")
	isAdmin := middleware.IsAdmin(c)

	render.Page(c, http.StatusOK, "teams-change_captain.html", gin.H{
		"GameID":        c.Param("game_id"),
		"TeamID":        req.TeamID,
		"Members":       members,
		"csrf":          csrf.GetToken(c),
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
	})
}

// ChangeCaptain производит смену капитана.
func (h *TeamHandler) ChangeCaptain(c *gin.Context) {
	var req TeamIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID команды")
		return
	}
	actorID := c.GetUint("userID")

	var input ChangeCaptainInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "teams-change_captain.html", gin.H{
			"Error": "Неверные данные: " + err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	// Валидация ID нового капитана
	if err := validation.ValidatePositiveUint("ID капитана", input.CaptainID); err != nil {
		render.Page(c, http.StatusBadRequest, "teams-change_captain.html", gin.H{
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	if err := h.teamService.ChangeCaptain(c.Request.Context(), req.TeamID, input.CaptainID, actorID); err != nil {
		log.Error().Err(err).Uint("team_id", req.TeamID).Uint("captain_id", input.CaptainID).Uint("actor_id", actorID).Msg("ChangeCaptain: failed to change captain")
		render.RenderError(c, http.StatusForbidden, err.Error())
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/teams/"+strconv.Itoa(int(req.TeamID))+"/members")
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
	var req TeamIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID команды")
		return
	}

	invitations, err := h.invitationService.ListByTeam(c.Request.Context(), req.TeamID)
	if err != nil {
		log.Error().Err(err).Uint("team_id", req.TeamID).Msg("InvitationHandler.Index: failed to list invitations")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	userID := c.GetUint("userID")
	isAdmin := middleware.IsAdmin(c)

	render.Page(c, http.StatusOK, "invitations-index.html", gin.H{
		"GameID":        c.Param("game_id"),
		"TeamID":        req.TeamID,
		"Invitations":   invitations,
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
	})
}

// NewForm показывает форму создания приглашения.
func (h *InvitationHandler) NewForm(c *gin.Context) {
	var req TeamIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID команды")
		return
	}

	userID := c.GetUint("userID")
	isAdmin := middleware.IsAdmin(c)

	render.Page(c, http.StatusOK, "invitations-new.html", gin.H{
		"GameID":        c.Param("game_id"),
		"TeamID":        req.TeamID,
		"csrf":          csrf.GetToken(c),
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
	})
}

// Create создаёт новое приглашение.
func (h *InvitationHandler) Create(c *gin.Context) {
	var req TeamIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID команды")
		return
	}
	userID := c.GetUint("userID")

	var input CreateInvitationInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "invitations-new.html", gin.H{
			"Error": "Неверный ID пользователя",
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	// Валидация ID пользователя
	if err := validation.ValidatePositiveUint("ID пользователя", input.UserID); err != nil {
		render.Page(c, http.StatusBadRequest, "invitations-new.html", gin.H{
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	_, err := h.invitationService.CreateInvitation(c.Request.Context(), req.TeamID, input.UserID, userID)
	if err != nil {
		log.Error().Err(err).Uint("team_id", req.TeamID).Uint("invited_user", input.UserID).Uint("inviter", userID).Msg("InvitationHandler.Create: failed to create invitation")
		render.Page(c, http.StatusInternalServerError, "invitations-new.html", gin.H{
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/teams/"+strconv.Itoa(int(req.TeamID))+"/invitations")
}

// MyInvitations отображает мои приглашения.
func (h *InvitationHandler) MyInvitations(c *gin.Context) {
	userID := c.GetUint("userID")
	invitations, err := h.invitationService.GetPendingForUser(c.Request.Context(), userID)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("MyInvitations: failed to get pending invitations")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	isAdmin := middleware.IsAdmin(c)

	render.Page(c, http.StatusOK, "invitations-my.html", gin.H{
		"Invitations":   invitations,
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
	})
}

// Accept принимает приглашение.
func (h *InvitationHandler) Accept(c *gin.Context) {
	var req InvitationIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID приглашения")
		return
	}
	userID := c.GetUint("userID")

	if err := h.invitationService.AcceptInvitation(c.Request.Context(), req.ID, userID); err != nil {
		log.Error().Err(err).Uint("invitation_id", req.ID).Uint("user_id", userID).Msg("Accept: failed to accept invitation")
		render.RenderError(c, http.StatusForbidden, err.Error())
		return
	}
	c.Redirect(http.StatusFound, "/invitations/my")
}

// Decline отклоняет приглашение.
func (h *InvitationHandler) Decline(c *gin.Context) {
	var req InvitationIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID приглашения")
		return
	}
	userID := c.GetUint("userID")

	if err := h.invitationService.DeclineInvitation(c.Request.Context(), req.ID, userID); err != nil {
		log.Error().Err(err).Uint("invitation_id", req.ID).Uint("user_id", userID).Msg("Decline: failed to decline invitation")
		render.RenderError(c, http.StatusForbidden, err.Error())
		return
	}
	c.Redirect(http.StatusFound, "/invitations/my")
}
