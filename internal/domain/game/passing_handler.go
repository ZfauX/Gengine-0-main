// internal/domain/game/passing_handler.go
package game

import (
	"net/http"
	"strconv"

	"gengine-0/internal/domain/team"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/storage"
	"gengine-0/internal/pkg/validation"

	csrf "gengine-0/internal/pkg/csrf"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// PassingHandler обрабатывает запросы, связанные с прохождениями игр.
type PassingHandler struct {
	passingService GamePassingServiceInterface
	gameAdminSvc   GameAdminServiceInterface
	coAuthorSvc    CoAuthorServiceInterface
	auditSvc       AuditServiceInterface
	storage        storage.FileStorage
}

// NewPassingHandler создаёт новый PassingHandler.
func NewPassingHandler(
	passingService GamePassingServiceInterface,
	gameAdminSvc GameAdminServiceInterface,
	coAuthorSvc CoAuthorServiceInterface,
	auditSvc AuditServiceInterface,
	storage storage.FileStorage,
) *PassingHandler {
	return &PassingHandler{
		passingService: passingService,
		gameAdminSvc:   gameAdminSvc,
		coAuthorSvc:    coAuthorSvc,
		auditSvc:       auditSvc,
		storage:        storage,
	}
}

// ListPassings отображает все заявки и прохождения игры.
func (h *PassingHandler) ListPassings(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID := c.GetUint("userID")

	passings, err := h.passingService.ListByGame(c.Request.Context(), uint(gameID))
	if err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("GameHandler.ListPassings: failed to list passings")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	isAdmin := middleware.IsAdmin(c)

	render.Page(c, http.StatusOK, "game_passings-list.html", gin.H{
		"GameID":        gameID,
		"Passings":      passings,
		"UserID":        userID,
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
		"csrf":          csrf.GetToken(c),
	})
}

// ApplyForm отображает форму подачи заявки.
func (h *PassingHandler) ApplyForm(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID := c.GetUint("userID")

	teams, err := h.passingService.GetTeamsByCaptain(c.Request.Context(), userID)
	if err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("PassingHandler.ApplyForm: failed to get teams")
		teams = []team.Team{}
	}

	isAdmin := middleware.IsAdmin(c)

	render.Page(c, http.StatusOK, "game_passings-apply.html", gin.H{
		"GameID":        gameID,
		"Teams":         teams,
		"csrf":          csrf.GetToken(c),
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
	})
}

// Apply подаёт заявку на игру.
func (h *PassingHandler) Apply(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID := c.GetUint("userID")

	renderTeams := func(teams []team.Team, errMsg string) {
		t, _ := h.passingService.GetTeamsByCaptain(c.Request.Context(), userID)
		if t != nil {
			teams = t
		}
		render.Page(c, http.StatusBadRequest, "game_passings-apply.html", gin.H{
			"GameID": gameID,
			"Teams":  teams,
			"Error":  errMsg,
			"csrf":   csrf.GetToken(c),
		})
	}

	if err := limitRequestBody(c, 1*1024*1024); err != nil {
		renderTeams(nil, err.Error())
		return
	}

	var input ApplyInput
	if err := c.ShouldBind(&input); err != nil {
		renderTeams(nil, "Неверные данные: "+err.Error())
		return
	}

	if err := validation.ValidatePositiveUint("ID команды", input.TeamID); err != nil {
		renderTeams(nil, err.Error())
		return
	}

	if err := h.passingService.Apply(c.Request.Context(), uint(gameID), input.TeamID, userID); err != nil {
		renderTeams(nil, err.Error())
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id"))
}

// UpdatePassingStatus изменяет статус заявки (принять/отклонить).
func (h *PassingHandler) UpdatePassingStatus(c *gin.Context) {
	passingID, err := strconv.Atoi(c.Param("passing_id"))
	if err != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID прохождения")
		return
	}
	status := GamePassingStatus(c.PostForm("status"))
	if status != StatusAccepted && status != StatusRejected {
		render.RenderError(c, http.StatusBadRequest, "Недопустимый статус")
		return
	}

	if err := h.passingService.UpdateStatus(c.Request.Context(), uint(passingID), status, c.GetUint("userID")); err != nil {
		render.RenderError(c, http.StatusForbidden, err.Error())
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/passings")
}

// StartGame запускает игру для конкретного прохождения.
func (h *PassingHandler) StartGame(c *gin.Context) {
	passingID, err := strconv.Atoi(c.Param("passing_id"))
	if err != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID прохождения")
		return
	}

	if err := h.passingService.StartGame(c.Request.Context(), uint(passingID), c.GetUint("userID")); err != nil {
		render.RenderError(c, http.StatusForbidden, err.Error())
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/monitor")
}

// ForceFinish принудительно завершает игру.
func (h *PassingHandler) ForceFinish(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID := c.GetUint("userID")

	if err := h.gameAdminSvc.ForceFinishGame(c.Request.Context(), uint(gameID), userID); err != nil {
		render.RenderError(c, http.StatusForbidden, err.Error())
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/results")
}

// DisqualifyTeam дисквалифицирует команду.
func (h *PassingHandler) DisqualifyTeam(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID := c.GetUint("userID")

	if err := limitRequestBody(c, 1*1024*1024); err != nil {
		render.RenderError(c, http.StatusBadRequest, err.Error())
		return
	}

	var input DisqualifyInput
	if err := c.ShouldBind(&input); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверные данные: "+err.Error())
		return
	}
	if err := validation.ValidatePositiveUint("ID команды", input.TeamID); err != nil {
		render.RenderError(c, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.gameAdminSvc.DisqualifyTeam(c.Request.Context(), uint(gameID), input.TeamID, userID); err != nil {
		render.RenderError(c, http.StatusForbidden, err.Error())
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/monitor")
}
