// internal/domain/team/handler.go
package team

import (
	"net/http"
	"strconv"

	"gengine-0/internal/pkg/storage"

	"github.com/gin-gonic/gin"
	csrf "github.com/utrack/gin-csrf"
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
// @Summary Список моих команд
// @Description Возвращает список команд, где пользователь является капитаном или участником
// @Tags teams
// @Produce html
// @Success 200 {string} html "Страница со списком команд"
// @Router /teams [get]
// @Security JWT
func (h *TeamHandler) MyTeams(c *gin.Context) {
	userID := c.GetUint("userID")
	teams, err := h.teamService.GetMyTeams(c.Request.Context(), userID)
	if err != nil {
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
// @Summary Форма создания команды
// @Description Возвращает HTML-страницу с формой для создания новой команды
// @Tags teams
// @Produce html
// @Success 200 {string} html "Форма создания команды"
// @Router /teams/new [get]
// @Security JWT
func (h *TeamHandler) NewTeamForm(c *gin.Context) {
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "teams-new.html",
		"csrf":         csrf.GetToken(c),
	})
}

// CreateTeam создаёт новую команду и делает текущего пользователя капитаном.
// @Summary Создание команды
// @Description Создаёт новую команду, текущий пользователь становится капитаном
// @Tags teams
// @Accept x-www-form-urlencoded
// @Produce html
// @Param name formData string true "Название команды (2-100 символов)"
// @Success 302 {string} string "Перенаправление на /teams"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Router /teams [post]
// @Security JWT
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
	_, err := h.teamService.CreateTeam(c.Request.Context(), input.Name, userID)
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
// @Summary Просмотр команды
// @Description Отображает информацию о команде и её составе
// @Tags teams
// @Produce html
// @Param team_id path int true "ID команды"
// @Success 200 {string} html "Страница команды"
// @Failure 404 {object} map[string]interface{} "Команда не найдена"
// @Router /teams/{team_id} [get]
// @Security JWT
func (h *TeamHandler) ViewTeam(c *gin.Context) {
	teamID, _ := strconv.Atoi(c.Param("team_id"))
	userID := c.GetUint("userID")

	team, members, err := h.teamService.GetTeamWithMembers(c.Request.Context(), uint(teamID))
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
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
// @Summary Состав команды (в контексте игры)
// @Description Отображает состав команды с возможностью управления (если есть права)
// @Tags teams
// @Produce html
// @Param game_id path int true "ID игры"
// @Param team_id path int true "ID команды"
// @Success 200 {string} html "Страница состава команды"
// @Failure 404 {object} map[string]interface{} "Команда не найдена"
// @Router /games/{game_id}/teams/{team_id}/members [get]
// @Security JWT
func (h *TeamHandler) Members(c *gin.Context) {
	teamID, _ := strconv.Atoi(c.Param("team_id"))
	userID := c.GetUint("userID")

	team, members, err := h.teamService.GetTeamWithMembers(c.Request.Context(), uint(teamID))
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
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
// @Summary Форма добавления участника
// @Description Возвращает HTML-страницу с формой для добавления участника в команду
// @Tags teams
// @Produce html
// @Param game_id path int true "ID игры"
// @Param team_id path int true "ID команды"
// @Success 200 {string} html "Форма добавления участника"
// @Router /games/{game_id}/teams/{team_id}/members/add [get]
// @Security JWT
func (h *TeamHandler) AddMemberForm(c *gin.Context) {
	teamID, _ := strconv.Atoi(c.Param("team_id"))
	availableUsers, err := h.teamService.GetAvailableUsers(c.Request.Context(), uint(teamID))
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
// @Summary Добавление участника
// @Description Добавляет нового участника в команду (доступно капитану или автору игры)
// @Tags teams
// @Accept x-www-form-urlencoded
// @Produce html
// @Param game_id path int true "ID игры"
// @Param team_id path int true "ID команды"
// @Param user_id formData uint true "ID пользователя"
// @Success 302 {string} string "Перенаправление на /games/{game_id}/teams/{team_id}/members"
// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
// @Router /games/{game_id}/teams/{team_id}/members [post]
// @Security JWT
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

	if err := h.teamService.AddMember(c.Request.Context(), uint(teamID), input.UserID, actorID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/teams/"+c.Param("team_id")+"/members")
}

// RemoveMember удаляет участника из команды.
// @Summary Удаление участника
// @Description Удаляет участника из команды (доступно капитану или автору игры)
// @Tags teams
// @Accept x-www-form-urlencoded
// @Produce html
// @Param game_id path int true "ID игры"
// @Param team_id path int true "ID команды"
// @Param member_id path int true "ID участника"
// @Success 302 {string} string "Перенаправление на /games/{game_id}/teams/{team_id}/members"
// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
// @Router /games/{game_id}/teams/{team_id}/members/{member_id} [delete]
// @Security JWT
func (h *TeamHandler) RemoveMember(c *gin.Context) {
	teamID, _ := strconv.Atoi(c.Param("team_id"))
	memberID, _ := strconv.Atoi(c.Param("member_id"))
	actorID := c.GetUint("userID")

	if err := h.teamService.RemoveMember(c.Request.Context(), uint(teamID), uint(memberID), actorID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/teams/"+c.Param("team_id")+"/members")
}

// ChangeCaptainForm показывает форму смены капитана.
// @Summary Форма смены капитана
// @Description Возвращает HTML-страницу с формой для смены капитана команды
// @Tags teams
// @Produce html
// @Param game_id path int true "ID игры"
// @Param team_id path int true "ID команды"
// @Success 200 {string} html "Форма смены капитана"
// @Router /games/{game_id}/teams/{team_id}/captain [get]
// @Security JWT
func (h *TeamHandler) ChangeCaptainForm(c *gin.Context) {
	teamID, _ := strconv.Atoi(c.Param("team_id"))
	_, members, err := h.teamService.GetTeamWithMembers(c.Request.Context(), uint(teamID))
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
// @Summary Смена капитана
// @Description Меняет капитана команды (доступно текущему капитану)
// @Tags teams
// @Accept x-www-form-urlencoded
// @Produce html
// @Param game_id path int true "ID игры"
// @Param team_id path int true "ID команды"
// @Param captain_id formData uint true "ID нового капитана"
// @Success 302 {string} string "Перенаправление на /games/{game_id}/teams/{team_id}/members"
// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
// @Router /games/{game_id}/teams/{team_id}/captain [post]
// @Security JWT
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

	if err := h.teamService.ChangeCaptain(c.Request.Context(), uint(teamID), input.CaptainID, actorID); err != nil {
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
// @Summary Список приглашений
// @Description Отображает список приглашений в команду
// @Tags invitations
// @Produce html
// @Param game_id path int true "ID игры"
// @Param team_id path int true "ID команды"
// @Success 200 {string} html "Страница со списком приглашений"
// @Router /games/{game_id}/teams/{team_id}/invitations [get]
// @Security JWT
func (h *InvitationHandler) Index(c *gin.Context) {
	teamID, _ := strconv.Atoi(c.Param("team_id"))
	invitations, err := h.invitationService.ListByTeam(c.Request.Context(), uint(teamID))
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
// @Summary Форма создания приглашения
// @Description Возвращает HTML-страницу с формой для создания приглашения
// @Tags invitations
// @Produce html
// @Param game_id path int true "ID игры"
// @Param team_id path int true "ID команды"
// @Success 200 {string} html "Форма создания приглашения"
// @Router /games/{game_id}/teams/{team_id}/invitations/new [get]
// @Security JWT
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
// @Summary Создание приглашения
// @Description Создаёт приглашение пользователя в команду (доступно капитану или автору игры)
// @Tags invitations
// @Accept x-www-form-urlencoded
// @Produce html
// @Param game_id path int true "ID игры"
// @Param team_id path int true "ID команды"
// @Param user_id formData uint true "ID приглашаемого пользователя"
// @Success 302 {string} string "Перенаправление на /games/{game_id}/teams/{team_id}/invitations"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Router /games/{game_id}/teams/{team_id}/invitations [post]
// @Security JWT
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

	_, err := h.invitationService.CreateInvitation(c.Request.Context(), uint(teamID), input.UserID, userID)
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
// @Summary Мои приглашения
// @Description Отображает список приглашений текущего пользователя
// @Tags invitations
// @Produce html
// @Success 200 {string} html "Страница с моими приглашениями"
// @Router /invitations/my [get]
// @Security JWT
func (h *InvitationHandler) MyInvitations(c *gin.Context) {
	userID := c.GetUint("userID")
	invitations, err := h.invitationService.GetPendingForUser(c.Request.Context(), userID)
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
// @Summary Принять приглашение
// @Description Принимает приглашение в команду
// @Tags invitations
// @Accept x-www-form-urlencoded
// @Produce html
// @Param id path int true "ID приглашения"
// @Success 302 {string} string "Перенаправление на /invitations/my"
// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
// @Router /invitations/{id}/accept [post]
// @Security JWT
func (h *InvitationHandler) Accept(c *gin.Context) {
	invitationID, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")
	if err := h.invitationService.AcceptInvitation(c.Request.Context(), uint(invitationID), userID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/invitations/my")
}

// Decline отклоняет приглашение.
// @Summary Отклонить приглашение
// @Description Отклоняет приглашение в команду
// @Tags invitations
// @Accept x-www-form-urlencoded
// @Produce html
// @Param id path int true "ID приглашения"
// @Success 302 {string} string "Перенаправление на /invitations/my"
// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
// @Router /invitations/{id}/decline [post]
// @Security JWT
func (h *InvitationHandler) Decline(c *gin.Context) {
	invitationID, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")
	if err := h.invitationService.DeclineInvitation(c.Request.Context(), uint(invitationID), userID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/invitations/my")
}
