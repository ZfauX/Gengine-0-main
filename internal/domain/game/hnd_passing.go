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

// ListPassings отображает список заявок и прохождений.
// @Summary Список заявок и прохождений
// @Tags passings
// @Produce html
// @Param id path int true "ID игры"
// @Success 200 {string} html "Список прохождений"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} render.Tr(c, "handler.forbidden")
// @Router /games/{id}/passings [get]
// @Security JWT
func (h *PassingHandler) ListPassings(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_game_id"))
		return
	}
	userID := c.GetUint("userID")

	// Check authorization — only managers, co-authors, and admins can view passings
	var isManager bool
	isManager, err = h.coAuthorSvc.IsUserManager(c.Request.Context(), uint(gameID), userID)
	if err != nil {
		log.Error().Err(err).Int("game_id", gameID).Uint("user", userID).Msg("ListPassings: auth check failed")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	} else if !isManager && !middleware.IsAdmin(c) {
		render.RenderErrorPage(c, http.StatusForbidden)
		return
	}

	perPage := 20
	page := 1
	if p, parseErr := strconv.Atoi(c.DefaultQuery("page", "1")); parseErr == nil && p > 0 {
		page = p
	}

	passings, totalItems, err := h.passingService.ListByGamePaginated(c.Request.Context(), uint(gameID), page, perPage)
	if err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("GameHandler.ListPassings: failed to list passings")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	totalPages := int((totalItems + int64(perPage) - 1) / int64(perPage))
	if totalPages < 1 {
		totalPages = 1
	}
	isAdmin := middleware.IsAdmin(c)

	render.Page(c, http.StatusOK, "game_passings-list.html", gin.H{
		"Title":         render.Tr(c, "nav.passings"),
		"GameID":        gameID,
		"Passings":      passings,
		"CurrentPage":   page,
		"TotalPages":    totalPages,
		"TotalItems":    totalItems,
		"UserID":        userID,
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
		"csrf":          csrf.GetToken(c),
		"Breadcrumbs": []map[string]string{
			{"name": "nav.home", "url": "/"},
			{"name": "nav.games", "url": "/games"},
			{"name": "game.breadcrumb_label", "url": "/games/" + c.Param("id")},
			{"name": "nav.passings"},
		},
	})
}

// ApplyForm отображает форму подачи заявки на игру.
// @Summary Подача заявки на игру (форма)
// @Tags passings
// @Produce html
// @Param id path int true "ID игры"
// @Success 200 {string} html "Форма заявки"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /games/{id}/apply [get]
// @Security JWT
func (h *PassingHandler) ApplyForm(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_game_id"))
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
		"Title":         "Подать заявку",
		"GameID":        gameID,
		"Teams":         teams,
		"csrf":          csrf.GetToken(c),
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
	})
}

// Apply подаёт заявку на участие в игре.
// @Summary Подача заявки на игру
// @Tags passings
// @Param id path int true "ID игры"
// @Param team_id formData int true "ID команды"
// @Success 302 {string} string "Перенаправление на /games/{id}"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} render.Tr(c, "handler.forbidden")
// @Router /games/{id}/apply [post]
// @Security JWT
func (h *PassingHandler) Apply(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_game_id"))
		return
	}
	userID := c.GetUint("userID")

	renderTeams := func(teams []team.Team, errMsg string) {
		t, _ := h.passingService.GetTeamsByCaptain(c.Request.Context(), userID)
		if t != nil {
			teams = t
		}
		render.Page(c, http.StatusBadRequest, "game_passings-apply.html", gin.H{
			"Title":  "Подать заявку",
			"GameID": gameID,
			"Teams":  teams,
			"Error":  errMsg,
			"csrf":   csrf.GetToken(c),
		})
	}

	if err := LimitRequestBody(c, 1*1024*1024); err != nil {
		renderTeams(nil, render.LocalizeError(c, err.Error()))
		return
	}

	var input ApplyInput
	if err := c.ShouldBind(&input); err != nil {
		log.Error().Err(err).Int("game_id", gameID).Uint("user", userID).Msg("PassingHandler.Apply: invalid input")
		renderTeams(nil, "Неверный формат данных")
		return
	}

	if err := validation.ValidatePositiveUint("ID команды", input.TeamID); err != nil {
		renderTeams(nil, render.LocalizeError(c, err.Error()))
		return
	}

	if err := h.passingService.Apply(c.Request.Context(), uint(gameID), input.TeamID, userID); err != nil {
		renderTeams(nil, render.LocalizeError(c, err.Error()))
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id"))
}

// UpdatePassingStatus изменяет статус заявки на участие.
// @Summary Изменение статуса заявки
// @Tags passings
// @Param id path int true "ID игры"
// @Param passing_id path int true "ID прохождения"
// @Param status formData string true "Статус (approved, rejected)"
// @Success 302 {string} string "Перенаправление на /games/{id}/passings"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} render.Tr(c, "handler.forbidden")
// @Router /games/{id}/passings/{passing_id}/status [post]
// @Security JWT
func (h *PassingHandler) UpdatePassingStatus(c *gin.Context) {
	passingID, err := strconv.Atoi(c.Param("passing_id"))
	if err != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_passing_id"))
		return
	}
	status := GamePassingStatus(c.PostForm("status"))
	if status != StatusAccepted && status != StatusRejected {
		render.RenderError(c, http.StatusBadRequest, "Недопустимый статус")
		return
	}

	if err := h.passingService.UpdateStatus(c.Request.Context(), uint(passingID), status, c.GetUint("userID")); err != nil {
		render.RenderError(c, http.StatusForbidden, render.LocalizeError(c, err.Error()))
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/passings")
}

// StartGame запускает игру для команды.
// @Summary Запуск игры
// @Tags passings
// @Param id path int true "ID игры"
// @Param passing_id path int true "ID прохождения"
// @Success 302 {string} string "Перенаправление на /games/{id}/passings"
// @Failure 400 {object} map[string]interface{} "Ошибка"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} render.Tr(c, "handler.forbidden")
// @Router /games/{id}/passings/{passing_id}/start [post]
// @Security JWT
func (h *PassingHandler) StartGame(c *gin.Context) {
	passingID, err := strconv.Atoi(c.Param("passing_id"))
	if err != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_passing_id"))
		return
	}

	if err := h.passingService.StartGame(c.Request.Context(), uint(passingID), c.GetUint("userID")); err != nil {
		render.RenderError(c, http.StatusForbidden, render.LocalizeError(c, err.Error()))
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/monitor")
}

// ForceFinish принудительно завершает игру для команды.
// @Summary Принудительное завершение игры
// @Tags admin
// @Accept x-www-form-urlencoded
// @Produce html
// @Param id path int true "ID игры"
// @Success 302 {string} string "Перенаправление на /games/{id}/passings"
// @Failure 400 {object} map[string]interface{} "Ошибка"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} render.Tr(c, "handler.forbidden")
// @Router /games/{id}/force-finish [post]
// @Security JWT
func (h *PassingHandler) ForceFinish(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_game_id"))
		return
	}
	userID := c.GetUint("userID")

	if err := h.gameAdminSvc.ForceFinishGame(c.Request.Context(), uint(gameID), userID); err != nil {
		ok, checkErr := h.coAuthorSvc.IsUserManager(c.Request.Context(), uint(gameID), userID)
		if checkErr == nil && ok {
			render.RenderError(c, http.StatusBadRequest, render.LocalizeError(c, err.Error()))
		} else {
			render.RenderError(c, http.StatusForbidden, render.LocalizeError(c, err.Error()))
		}
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/results")
}

// DisqualifyTeam дисквалифицирует команду.
// @Summary Дисквалификация команды
// @Tags admin
// @Accept x-www-form-urlencoded
// @Produce html
// @Param id path int true "ID игры"
// @Success 302 {string} string "Перенаправление на /games/{id}/passings"
// @Failure 400 {object} map[string]interface{} "Ошибка"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} render.Tr(c, "handler.forbidden")
// @Router /games/{id}/disqualify [post]
// @Security JWT
func (h *PassingHandler) DisqualifyTeam(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_game_id"))
		return
	}
	userID := c.GetUint("userID")

	if err := LimitRequestBody(c, 1*1024*1024); err != nil {
		render.RenderError(c, http.StatusBadRequest, render.LocalizeError(c, err.Error()))
		return
	}

	var input DisqualifyInput
	if err := c.ShouldBind(&input); err != nil {
		render.RenderError(c, http.StatusBadRequest, render.LocalizeError(c, err.Error()))
		return
	}
	if err := validation.ValidatePositiveUint("ID команды", input.TeamID); err != nil {
		render.RenderError(c, http.StatusBadRequest, render.LocalizeError(c, err.Error()))
		return
	}

	if err := h.gameAdminSvc.DisqualifyTeam(c.Request.Context(), uint(gameID), input.TeamID, userID); err != nil {
		render.RenderError(c, http.StatusForbidden, render.LocalizeError(c, err.Error()))
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/monitor")
}
