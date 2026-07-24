// internal/domain/tournament/handler.go
package tournament

import (
	"errors"
	"net/http"
	"strconv"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/sanitize"
	"gengine-0/internal/pkg/validation"

	csrf "gengine-0/internal/pkg/csrf"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// ---------- Входные структуры для валидации ----------

// TournamentIDRequest используется для валидации ID турнира в URL.
type TournamentIDRequest struct {
	ID uint `uri:"id" binding:"required,gt=0"`
}

// TournamentGameIDRequest используется для валидации ID турнира и игры.
type TournamentGameIDRequest struct {
	ID     uint `uri:"id" binding:"required,gt=0"`
	GameID uint `uri:"game_id" binding:"required,gt=0"`
}

// CreateTournamentInput используется для создания турнира.
type CreateTournamentInput struct {
	Name                   string `form:"name" binding:"required,min=2,max=200"`
	Description            string `form:"description" binding:"max=5000"`
	PointsForFirst         int    `form:"points_for_first" binding:"min=0"`
	PointsForSecond        int    `form:"points_for_second" binding:"min=0"`
	PointsForThird         int    `form:"points_for_third" binding:"min=0"`
	PointsForParticipation int    `form:"points_for_participation" binding:"min=0"`
}

// UpdateTournamentInput используется для обновления турнира.
type UpdateTournamentInput struct {
	Name                   string `form:"name" binding:"omitempty,min=2,max=200"`
	Description            string `form:"description" binding:"max=5000"`
	PointsForFirst         int    `form:"points_for_first" binding:"min=0"`
	PointsForSecond        int    `form:"points_for_second" binding:"min=0"`
	PointsForThird         int    `form:"points_for_third" binding:"min=0"`
	PointsForParticipation int    `form:"points_for_participation" binding:"min=0"`
}

// AddGameInput используется для добавления игры в турнир.
type AddGameInput struct {
	GameID uint `form:"game_id" binding:"required,gt=0"`
}

// ApplyInput используется для подачи заявки на турнир.
type ApplyInput struct {
	TeamID uint `form:"team_id" binding:"required,gt=0"`
}

// ---------- Обработчики ----------

type TournamentHandler struct {
	tournamentService *TournamentService
	teamService       *team.TeamService
	cfg               *config.Config
}

func NewTournamentHandler(
	tournamentService *TournamentService,
	teamService *team.TeamService,
	cfg *config.Config,
) *TournamentHandler {
	return &TournamentHandler{
		tournamentService: tournamentService,
		teamService:       teamService,
		cfg:               cfg,
	}
}

// List отображает список турниров.
// List отображает список турниров.
// @Summary Список турниров
// @Description Возвращает HTML-страницу со списком всех турниров с пагинацией
// @Tags tournaments
// @Produce html
// @Success 200 {string} html "Страница списка турниров"
// @Router /tournaments [get]
func (h *TournamentHandler) List(c *gin.Context) {
	tournaments, err := h.tournamentService.List(c.Request.Context())
	if err != nil {
		log.Error().Err(err).Msg("TournamentHandler.List: failed to list tournaments")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	render.Page(c, http.StatusOK, "tournaments-list.html", gin.H{
		"Tournaments": tournaments,
		"Breadcrumbs": []map[string]string{
			{"name": "Главная", "url": "/"},
			{"name": "Турниры"},
		},
	})
}

// Show отображает детальную информацию о турнире.
// Show отображает один турнир с таблицей лидеров.
// @Summary Детали турнира
// @Description Отображает информацию о турнире: описание, даты, список игр, заявки
// @Tags tournaments
// @Produce html
// @Param id path int true "ID турнира"
// @Success 200 {string} html "Страница турнира"
// @Failure 404 {object} map[string]interface{} "Турнир не найден"
// @Router /tournaments/{id} [get]
func (h *TournamentHandler) Show(c *gin.Context) {
	var req TournamentIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID турнира")
		return
	}
	userID := c.GetUint("userID")

	t, err := h.tournamentService.GetByID(c.Request.Context(), req.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			render.RenderErrorPage(c, http.StatusNotFound)
		} else {
			log.Error().Err(err).Uint("tournament_id", req.ID).Msg("TournamentHandler.Show: failed to get tournament")
			render.RenderErrorPage(c, http.StatusInternalServerError)
		}
		return
	}

	games, err := h.tournamentService.ListGames(c.Request.Context(), req.ID)
	if err != nil {
		log.Error().Err(err).Uint("tournament_id", req.ID).Msg("TournamentHandler.Show: failed to list games")
		games = []game.Game{} // fallback
	}

	leaderboard, err := h.tournamentService.GetLeaderboard(c.Request.Context(), req.ID)
	if err != nil {
		log.Error().Err(err).Uint("tournament_id", req.ID).Msg("TournamentHandler.Show: failed to get leaderboard")
		leaderboard = []TournamentResult{}
	}

	canApply := h.tournamentService.CanApply(c.Request.Context(), req.ID, userID)

	render.Page(c, http.StatusOK, "tournaments-show.html", gin.H{
		"Tournament":    t,
		"Games":         games,
		"Leaderboard":   leaderboard,
		"CanApply":      canApply,
		"CurrentUserID": userID,
		"csrf":          csrf.GetToken(c),
		"Breadcrumbs": []map[string]string{
			{"name": "Главная", "url": "/"},
			{"name": "Турниры", "url": "/tournaments"},
			{"name": t.Name},
		},
	})
}

// NewForm отображает форму создания турнира.
// NewForm отображает форму создания турнира.
// @Summary Форма создания турнира
// @Tags tournaments
// @Produce html
// @Success 200 {string} html "Форма создания"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /tournaments/new [get]
// @Security JWT
func (h *TournamentHandler) NewForm(c *gin.Context) {
	render.Page(c, http.StatusOK, "tournaments-new.html", gin.H{
		"csrf": csrf.GetToken(c),
		"Breadcrumbs": []map[string]string{
			{"name": "Главная", "url": "/"},
			{"name": "Турниры", "url": "/tournaments"},
			{"name": "Создание турнира"},
		},
	})
}

// Create создаёт новый турнир.
// Create создаёт новый турнир.
// @Summary Создание турнира
// @Tags tournaments
// @Accept x-www-form-urlencoded
// @Produce html
// @Param name formData string true "Название турнира"
// @Param description formData string false "Описание турнира"
// @Param starts_at formData string true "Дата начала"
// @Param ends_at formData string true "Дата окончания"
// @Success 302 {string} string "Перенаправление на страницу турнира"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /tournaments [post]
// @Security JWT
func (h *TournamentHandler) Create(c *gin.Context) {
	userID := c.GetUint("userID")

	var input CreateTournamentInput
	errs := validation.FieldErrors{}
	if err := c.ShouldBind(&input); err != nil {
		errs.Add("name", validation.ValidateString("Название", input.Name, 2, 200))
		errs.Add("description", validation.ValidateString("Описание", input.Description, 0, 5000))
		if input.PointsForFirst < 0 {
			errs.Add("points_for_first", errors.New("очки за первое место не могут быть отрицательными"))
		}
		if input.PointsForSecond < 0 {
			errs.Add("points_for_second", errors.New("очки за второе место не могут быть отрицательными"))
		}
		if input.PointsForThird < 0 {
			errs.Add("points_for_third", errors.New("очки за третье место не могут быть отрицательными"))
		}
		if input.PointsForParticipation < 0 {
			errs.Add("points_for_participation", errors.New("очки за участие не могут быть отрицательными"))
		}
		if !errs.HasErrors() {
			errs.Add("form", err)
		}
		render.Page(c, http.StatusBadRequest, "tournaments-new.html", gin.H{
			"Error":  errs.Error(),
			"Errors": errs,
			"csrf":   csrf.GetToken(c),
		})
		return
	}

	t := &Tournament{
		Name:                   sanitize.StripHTML(input.Name),
		Description:            sanitize.StripHTML(input.Description),
		AuthorID:               userID,
		PointsForFirst:         input.PointsForFirst,
		PointsForSecond:        input.PointsForSecond,
		PointsForThird:         input.PointsForThird,
		PointsForParticipation: input.PointsForParticipation,
	}

	if err := h.tournamentService.Create(c.Request.Context(), t); err != nil {
		log.Error().Err(err).Uint("author_id", userID).Msg("TournamentHandler.Create: failed to create tournament")
		render.Page(c, http.StatusInternalServerError, "tournaments-new.html", gin.H{
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	c.Redirect(http.StatusFound, "/tournaments/"+strconv.Itoa(int(t.ID)))
}

// EditForm отображает форму редактирования турнира.
// EditForm отображает форму редактирования турнира.
// @Summary Форма редактирования турнира
// @Tags tournaments
// @Produce html
// @Param id path int true "ID турнира"
// @Success 200 {string} html "Форма редактирования"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Failure 404 {object} map[string]interface{} "Турнир не найден"
// @Router /tournaments/{id}/edit [get]
// @Security JWT
func (h *TournamentHandler) EditForm(c *gin.Context) {
	var req TournamentIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID турнира")
		return
	}
	userID := c.GetUint("userID")

	t, err := h.tournamentService.GetByID(c.Request.Context(), req.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			render.RenderErrorPage(c, http.StatusNotFound)
		} else {
			log.Error().Err(err).Uint("tournament_id", req.ID).Msg("TournamentHandler.EditForm: failed to get tournament")
			render.RenderErrorPage(c, http.StatusInternalServerError)
		}
		return
	}
	if t.AuthorID != userID {
		render.RenderErrorPage(c, http.StatusForbidden)
		return
	}

	render.Page(c, http.StatusOK, "tournaments-edit.html", gin.H{
		"Tournament": t,
		"csrf":       csrf.GetToken(c),
		"Breadcrumbs": []map[string]string{
			{"name": "Главная", "url": "/"},
			{"name": "Турниры", "url": "/tournaments"},
			{"name": t.Name, "url": "/tournaments/" + strconv.Itoa(int(t.ID))},
			{"name": "Редактирование"},
		},
	})
}

// Update обновляет турнир.
// Update обновляет турнир.
// @Summary Обновление турнира
// @Tags tournaments
// @Accept x-www-form-urlencoded
// @Produce html
// @Param id path int true "ID турнира"
// @Param name formData string false "Название турнира"
// @Param description formData string false "Описание турнира"
// @Param starts_at formData string false "Дата начала"
// @Param ends_at formData string false "Дата окончания"
// @Success 302 {string} string "Перенаправление на страницу турнира"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Router /tournaments/{id} [put]
// @Security JWT
func (h *TournamentHandler) Update(c *gin.Context) {
	var req TournamentIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID турнира")
		return
	}
	userID := c.GetUint("userID")

	var input UpdateTournamentInput
	errs := validation.FieldErrors{}
	if err := c.ShouldBind(&input); err != nil {
		if input.Name != "" {
			errs.Add("name", validation.ValidateString("Название", input.Name, 2, 200))
		}
		errs.Add("description", validation.ValidateString("Описание", input.Description, 0, 5000))
		if input.PointsForFirst < 0 {
			errs.Add("points_for_first", errors.New("очки за первое место не могут быть отрицательными"))
		}
		if input.PointsForSecond < 0 {
			errs.Add("points_for_second", errors.New("очки за второе место не могут быть отрицательными"))
		}
		if input.PointsForThird < 0 {
			errs.Add("points_for_third", errors.New("очки за третье место не могут быть отрицательными"))
		}
		if input.PointsForParticipation < 0 {
			errs.Add("points_for_participation", errors.New("очки за участие не могут быть отрицательными"))
		}
		if !errs.HasErrors() {
			errs.Add("form", err)
		}
		render.Page(c, http.StatusBadRequest, "tournaments-edit.html", gin.H{
			"Error":  errs.Error(),
			"Errors": errs,
			"csrf":   csrf.GetToken(c),
		})
		return
	}

	updated := &Tournament{
		Name:                   sanitize.StripHTML(input.Name),
		Description:            sanitize.StripHTML(input.Description),
		PointsForFirst:         input.PointsForFirst,
		PointsForSecond:        input.PointsForSecond,
		PointsForThird:         input.PointsForThird,
		PointsForParticipation: input.PointsForParticipation,
	}

	if err := h.tournamentService.Update(c.Request.Context(), req.ID, updated, userID); err != nil {
		log.Error().Err(err).Uint("tournament_id", req.ID).Uint("user_id", userID).Msg("TournamentHandler.Update: failed to update tournament")
		render.Page(c, http.StatusInternalServerError, "tournaments-edit.html", gin.H{
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	c.Redirect(http.StatusFound, "/tournaments/"+strconv.Itoa(int(req.ID)))
}

// Games отображает список игр турнира.
// Games отображает список игр турнира.
// @Summary Список игр турнира
// @Tags tournaments
// @Produce html
// @Param id path int true "ID турнира"
// @Success 200 {string} html "Список игр турнира"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Router /tournaments/{id}/games [get]
// @Security JWT
func (h *TournamentHandler) Games(c *gin.Context) {
	var req TournamentIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID турнира")
		return
	}
	userID := c.GetUint("userID")

	games, err := h.tournamentService.ListGames(c.Request.Context(), req.ID)
	if err != nil {
		log.Error().Err(err).Uint("tournament_id", req.ID).Msg("TournamentHandler.Games: failed to list games")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	availableGames, err := h.tournamentService.GetAvailableGames(c.Request.Context(), req.ID, userID)
	if err != nil {
		log.Error().Err(err).Uint("tournament_id", req.ID).Uint("user_id", userID).Msg("TournamentHandler.Games: failed to get available games")
		availableGames = []game.Game{}
	}

	render.Page(c, http.StatusOK, "tournaments-games.html", gin.H{
		"TournamentID":   req.ID,
		"Games":          games,
		"AvailableGames": availableGames,
		"csrf":           csrf.GetToken(c),
	})
}

// AddGame добавляет игру в турнир.
// AddGame добавляет игру в турнир.
// @Summary Добавление игры в турнир
// @Tags tournaments
// @Param id path int true "ID турнира"
// @Param game_id formData int true "ID игры"
// @Success 302 {string} string "Перенаправление на /tournaments/{id}/games"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Router /tournaments/{id}/games [post]
// @Security JWT
func (h *TournamentHandler) AddGame(c *gin.Context) {
	var req TournamentGameIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверные данные")
		return
	}
	userID := c.GetUint("userID")

	var input AddGameInput
	if err := c.ShouldBind(&input); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверные данные: "+err.Error())
		return
	}

	// Валидация ID игры
	if err := validation.ValidatePositiveUint("ID игры", input.GameID); err != nil {
		render.RenderError(c, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.tournamentService.AddGame(c.Request.Context(), req.ID, input.GameID, userID); err != nil {
		log.Error().Err(err).Uint("tournament_id", req.ID).Uint("game_id", input.GameID).Uint("user_id", userID).Msg("TournamentHandler.AddGame: failed to add game")
		render.RenderError(c, http.StatusForbidden, err.Error())
		return
	}

	c.Redirect(http.StatusFound, "/tournaments/"+strconv.Itoa(int(req.ID))+"/games")
}

// RemoveGame удаляет игру из турнира.
// RemoveGame удаляет игру из турнира.
// @Summary Удаление игры из турнира
// @Tags tournaments
// @Param id path int true "ID турнира"
// @Param game_id path int true "ID игры"
// @Success 302 {string} string "Перенаправление на /tournaments/{id}/games"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Router /tournaments/{id}/games/{game_id} [delete]
// @Security JWT
func (h *TournamentHandler) RemoveGame(c *gin.Context) {
	var req TournamentGameIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверные данные")
		return
	}
	userID := c.GetUint("userID")

	if err := h.tournamentService.RemoveGame(c.Request.Context(), req.ID, req.GameID, userID); err != nil {
		log.Error().Err(err).Uint("tournament_id", req.ID).Uint("game_id", req.GameID).Uint("user_id", userID).Msg("TournamentHandler.RemoveGame: failed to remove game")
		render.RenderError(c, http.StatusForbidden, err.Error())
		return
	}

	c.Redirect(http.StatusFound, "/tournaments/"+strconv.Itoa(int(req.ID))+"/games")
}

// ApplyForm отображает форму подачи заявки на турнир.
// ApplyForm отображает форму подачи заявки на турнир.
// @Summary Форма подачи заявки на турнир
// @Tags tournaments
// @Produce html
// @Param id path int true "ID турнира"
// @Success 200 {string} html "Форма заявки"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /tournaments/{id}/apply [get]
// @Security JWT
func (h *TournamentHandler) ApplyForm(c *gin.Context) {
	var req TournamentIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID турнира")
		return
	}
	userID := c.GetUint("userID")

	teams, err := h.teamService.GetMyTeams(c.Request.Context(), userID)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("TournamentHandler.ApplyForm: failed to get teams")
		teams = []team.Team{}
	}

	render.Page(c, http.StatusOK, "tournaments-apply.html", gin.H{
		"TournamentID": req.ID,
		"Teams":        teams,
		"csrf":         csrf.GetToken(c),
	})
}

// Apply подаёт заявку на участие в турнире.
// Apply подаёт заявку на турнир.
// @Summary Подача заявки на турнир
// @Tags tournaments
// @Param id path int true "ID турнира"
// @Param team_id formData int true "ID команды"
// @Success 302 {string} string "Перенаправление на страницу турнира"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Router /tournaments/{id}/apply [post]
// @Security JWT
func (h *TournamentHandler) Apply(c *gin.Context) {
	var req TournamentIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID турнира")
		return
	}
	userID := c.GetUint("userID")

	var input ApplyInput
	if err := c.ShouldBind(&input); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверные данные: "+err.Error())
		return
	}

	// Валидация ID команды
	if err := validation.ValidatePositiveUint("ID команды", input.TeamID); err != nil {
		render.RenderError(c, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.tournamentService.Apply(c.Request.Context(), req.ID, input.TeamID, userID); err != nil {
		log.Error().Err(err).Uint("tournament_id", req.ID).Uint("team_id", input.TeamID).Uint("user_id", userID).Msg("TournamentHandler.Apply: failed to apply")
		render.RenderError(c, http.StatusForbidden, err.Error())
		return
	}

	c.Redirect(http.StatusFound, "/tournaments/"+strconv.Itoa(int(req.ID)))
}
