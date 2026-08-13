// internal/domain/game/passing_handler.go
package game

import (
	"errors"
	"net/http"
	"strconv"
	"time"

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
// @Failure 403 {object} map[string]interface{} "handler.forbidden"
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
// @Failure 403 {object} map[string]interface{} "handler.forbidden"
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
// @Failure 403 {object} map[string]interface{} "handler.forbidden"
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
// @Failure 403 {object} map[string]interface{} "handler.forbidden"
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
// @Failure 403 {object} map[string]interface{} "handler.forbidden"
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
// @Failure 403 {object} map[string]interface{} "handler.forbidden"
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

// Фаза 3 (C-1..C-5, pass 45) --------------------------------

// SetTeamRoute назначает маршрут команды (C-1/C-2).
// @Summary Назначение маршрута команды
// @Tags passings
// @Param id path int true "ID игры"
// @Param passing_id path int true "ID прохождения"
// @Param level_ids formData []uint true "Порядок уровней"
// @Success 302 {string} string "Редирект"
// @Security JWT
func (h *PassingHandler) SetTeamRoute(c *gin.Context) {
	var req struct {
		GameID    int    `uri:"id" binding:"required"`
		PassingID int    `uri:"passing_id" binding:"required"`
		LevelIDs  []uint `form:"level_ids"`
	}
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_id"))
		return
	}
	if err := c.ShouldBind(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_data"))
		return
	}
	// DEEP-REVIEW PASS-2 (#3): observer (read-only соавтор) не должен менять
	// маршруты команд — требуется право редактирования контента.
	if !h.requireEditContent(c, uint(req.GameID)) {
		return
	}
	if err := h.passingService.SetTeamRoute(c.Request.Context(), uint(req.GameID), uint(req.PassingID), req.LevelIDs); err != nil {
		log.Error().Err(err).Int("passing_id", req.PassingID).Msg("SetTeamRoute: failed")
		if errors.Is(err, ErrPassingNotInGame) {
			render.RenderError(c, http.StatusForbidden, render.Tr(c, "handler.forbidden"))
			return
		}
		render.RenderError(c, http.StatusInternalServerError, render.LocalizeError(c, err.Error()))
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/passings")
}

// GetTeamRoute возвращает маршрут команды (JSON).
func (h *PassingHandler) GetTeamRoute(c *gin.Context) {
	var req struct {
		GameID    int `uri:"id" binding:"required"`
		PassingID int `uri:"passing_id" binding:"required"`
	}
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_id"))
		return
	}
	route, err := h.passingService.GetTeamRoute(c.Request.Context(), uint(req.GameID), uint(req.PassingID))
	if err != nil {
		log.Error().Err(err).Int("game_id", req.GameID).Int("passing_id", req.PassingID).Msg("GetTeamRoute: failed")
		render.RenderError(c, http.StatusInternalServerError, render.LocalizeError(c, err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"route": route})
}

// SetTeamStartTime задаёт индивидуальное время старта команды (C-3).
func (h *PassingHandler) SetTeamStartTime(c *gin.Context) {
	var req struct {
		GameID    int `uri:"id" binding:"required"`
		PassingID int `uri:"passing_id" binding:"required"`
	}
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_id"))
		return
	}
	// DEEP-REVIEW PASS-2 (#3): observer не меняет время старта команд.
	if !h.requireEditContent(c, uint(req.GameID)) {
		return
	}
	startTimeStr := c.PostForm("start_time")
	var startTime *time.Time
	if startTimeStr != "" {
		if t, err := time.Parse("2006-01-02T15:04", startTimeStr); err == nil {
			startTime = &t
		} else {
			render.RenderError(c, http.StatusBadRequest, "Неверный формат времени старта")
			return
		}
	}
	if err := h.passingService.SetTeamStartTime(c.Request.Context(), uint(req.GameID), uint(req.PassingID), startTime); err != nil {
		log.Error().Err(err).Int("passing_id", req.PassingID).Msg("SetTeamStartTime: failed")
		if errors.Is(err, ErrPassingNotInGame) {
			render.RenderError(c, http.StatusForbidden, render.Tr(c, "handler.forbidden"))
			return
		}
		render.RenderError(c, http.StatusInternalServerError, render.LocalizeError(c, err.Error()))
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/passings")
}

// SetTeamAnswer задаёт персональный ответ уровня для команды (C-4).
func (h *PassingHandler) SetTeamAnswer(c *gin.Context) {
	var req struct {
		GameID  int `uri:"id" binding:"required"`
		LevelID int `uri:"level_id" binding:"required"`
		TeamID  int `uri:"team_id" binding:"required"`
	}
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_id"))
		return
	}
	// DEEP-REVIEW PASS-2 (#3): observer не может подделывать ответы команд.
	if !h.requireEditContent(c, uint(req.GameID)) {
		return
	}
	code := c.PostForm("code")
	hint := c.PostForm("hint")
	if code == "" {
		render.RenderError(c, http.StatusBadRequest, "Укажите код ответа")
		return
	}
	if err := h.passingService.SetTeamAnswer(c.Request.Context(), uint(req.GameID), uint(req.LevelID), uint(req.TeamID), code, hint); err != nil {
		log.Error().Err(err).Int("level_id", req.LevelID).Int("team_id", req.TeamID).Msg("SetTeamAnswer: failed")
		if errors.Is(err, ErrLevelNotInGame) {
			render.RenderError(c, http.StatusForbidden, render.Tr(c, "handler.forbidden"))
			return
		}
		render.RenderError(c, http.StatusInternalServerError, render.LocalizeError(c, err.Error()))
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/levels/"+strconv.Itoa(req.LevelID)+"/passings")
}

// AttemptsPerUser возвращает количество найденных кодов по игрокам (C-5).
func (h *PassingHandler) AttemptsPerUser(c *gin.Context) {
	var req struct {
		GameID int `uri:"id" binding:"required"`
	}
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_id"))
		return
	}
	rows, err := h.passingService.GetAttemptsPerUser(c.Request.Context(), uint(req.GameID))
	if err != nil {
		log.Error().Err(err).Int("game_id", req.GameID).Msg("AttemptsPerUser: failed")
		render.RenderError(c, http.StatusInternalServerError, render.LocalizeError(c, err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"attempts_per_user": rows})
}

// requireEditContent проверяет право соавтора на редактирование контента игры
// (DEEP-REVIEW PASS-2 #3): IsUserManager пропускает observer (read-only), а
// Phase-3 операции (маршруты, время старта, ответы) требуют RoleContentEditor.
// Возвращает false и пишет 403, если права нет.
func (h *PassingHandler) requireEditContent(c *gin.Context, gameID uint) bool {
	userID := c.GetUint("userID")
	ok, err := h.coAuthorSvc.CanEditContent(c.Request.Context(), gameID, userID)
	if err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Uint("user_id", userID).Msg("requireEditContent: permission check error")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return false
	}
	if !ok {
		render.RenderErrorPage(c, http.StatusForbidden)
		return false
	}
	return true
}
