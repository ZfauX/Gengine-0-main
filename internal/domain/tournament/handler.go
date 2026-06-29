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

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	csrf "github.com/utrack/gin-csrf"
	"gorm.io/gorm"
)

// ---------- Входные структуры ----------

type CreateTournamentInput struct {
	Name                   string `form:"name" binding:"required,min=2,max=200"`
	Description            string `form:"description" binding:"max=5000"`
	PointsForFirst         int    `form:"points_for_first"`
	PointsForSecond        int    `form:"points_for_second"`
	PointsForThird         int    `form:"points_for_third"`
	PointsForParticipation int    `form:"points_for_participation"`
}

type UpdateTournamentInput struct {
	Name                   string `form:"name" binding:"min=2,max=200"`
	Description            string `form:"description" binding:"max=5000"`
	PointsForFirst         int    `form:"points_for_first"`
	PointsForSecond        int    `form:"points_for_second"`
	PointsForThird         int    `form:"points_for_third"`
	PointsForParticipation int    `form:"points_for_participation"`
}

type AddGameInput struct {
	GameID uint `form:"game_id" binding:"required,gt=0"`
}

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
func (h *TournamentHandler) List(c *gin.Context) {
	tournaments, err := h.tournamentService.List(c.Request.Context())
	if err != nil {
		log.Error().Err(err).Msg("TournamentHandler.List: failed to list tournaments")
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}
	render.Page(c, http.StatusOK, "tournaments-list.html", gin.H{
		"Tournaments": tournaments,
	})
}

// Show отображает один турнир с таблицей лидеров.
func (h *TournamentHandler) Show(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID турнира"})
		return
	}
	userID := c.GetUint("userID")

	t, err := h.tournamentService.GetByID(c.Request.Context(), uint(id))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.HTML(http.StatusNotFound, "errors/404.html", nil)
		} else {
			log.Error().Err(err).Int("tournament_id", id).Msg("TournamentHandler.Show: failed to get tournament")
			c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		}
		return
	}

	games, err := h.tournamentService.ListGames(c.Request.Context(), uint(id))
	if err != nil {
		log.Error().Err(err).Int("tournament_id", id).Msg("TournamentHandler.Show: failed to list games")
		games = []game.Game{} // fallback
	}

	leaderboard, err := h.tournamentService.GetLeaderboard(c.Request.Context(), uint(id))
	if err != nil {
		log.Error().Err(err).Int("tournament_id", id).Msg("TournamentHandler.Show: failed to get leaderboard")
		leaderboard = []TournamentResult{}
	}

	canApply := h.tournamentService.CanApply(c.Request.Context(), uint(id), userID)

	render.Page(c, http.StatusOK, "tournaments-show.html", gin.H{
		"Tournament":    t,
		"Games":         games,
		"Leaderboard":   leaderboard,
		"CanApply":      canApply,
		"CurrentUserID": userID,
		"csrf":          csrf.GetToken(c),
	})
}

// NewForm отображает форму создания турнира.
func (h *TournamentHandler) NewForm(c *gin.Context) {
	render.Page(c, http.StatusOK, "tournaments-new.html", gin.H{
		"csrf": csrf.GetToken(c),
	})
}

// Create создаёт новый турнир.
func (h *TournamentHandler) Create(c *gin.Context) {
	userID := c.GetUint("userID")

	var input CreateTournamentInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "tournaments-new.html", gin.H{
			"Error": "Неверные данные: " + err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	t := &Tournament{
		Name:                   input.Name,
		Description:            input.Description,
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
func (h *TournamentHandler) EditForm(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID турнира"})
		return
	}
	userID := c.GetUint("userID")

	t, err := h.tournamentService.GetByID(c.Request.Context(), uint(id))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.HTML(http.StatusNotFound, "errors/404.html", nil)
		} else {
			log.Error().Err(err).Int("tournament_id", id).Msg("TournamentHandler.EditForm: failed to get tournament")
			c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		}
		return
	}
	if t.AuthorID != userID {
		c.HTML(http.StatusForbidden, "errors/403.html", nil)
		return
	}

	render.Page(c, http.StatusOK, "tournaments-edit.html", gin.H{
		"Tournament": t,
		"csrf":       csrf.GetToken(c),
	})
}

// Update обновляет турнир.
func (h *TournamentHandler) Update(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID турнира"})
		return
	}
	userID := c.GetUint("userID")

	var input UpdateTournamentInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "tournaments-edit.html", gin.H{
			"Error": "Неверные данные: " + err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	updated := &Tournament{
		Name:                   input.Name,
		Description:            input.Description,
		PointsForFirst:         input.PointsForFirst,
		PointsForSecond:        input.PointsForSecond,
		PointsForThird:         input.PointsForThird,
		PointsForParticipation: input.PointsForParticipation,
	}

	if err := h.tournamentService.Update(c.Request.Context(), uint(id), updated, userID); err != nil {
		log.Error().Err(err).Int("tournament_id", id).Uint("user_id", userID).Msg("TournamentHandler.Update: failed to update tournament")
		render.Page(c, http.StatusInternalServerError, "tournaments-edit.html", gin.H{
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	c.Redirect(http.StatusFound, "/tournaments/"+strconv.Itoa(id))
}

// Games отображает список игр турнира.
func (h *TournamentHandler) Games(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID турнира"})
		return
	}
	userID := c.GetUint("userID")

	games, err := h.tournamentService.ListGames(c.Request.Context(), uint(id))
	if err != nil {
		log.Error().Err(err).Int("tournament_id", id).Msg("TournamentHandler.Games: failed to list games")
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}

	availableGames, err := h.tournamentService.GetAvailableGames(c.Request.Context(), uint(id), userID)
	if err != nil {
		log.Error().Err(err).Int("tournament_id", id).Uint("user_id", userID).Msg("TournamentHandler.Games: failed to get available games")
		availableGames = []game.Game{}
	}

	render.Page(c, http.StatusOK, "tournaments-games.html", gin.H{
		"TournamentID":   id,
		"Games":          games,
		"AvailableGames": availableGames,
		"csrf":           csrf.GetToken(c),
	})
}

// AddGame добавляет игру в турнир.
func (h *TournamentHandler) AddGame(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID турнира"})
		return
	}
	userID := c.GetUint("userID")

	var input AddGameInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверные данные: " + err.Error()})
		return
	}

	if err := h.tournamentService.AddGame(c.Request.Context(), uint(id), input.GameID, userID); err != nil {
		log.Error().Err(err).Int("tournament_id", id).Uint("game_id", input.GameID).Uint("user_id", userID).Msg("TournamentHandler.AddGame: failed to add game")
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, "/tournaments/"+strconv.Itoa(id)+"/games")
}

// RemoveGame удаляет игру из турнира.
func (h *TournamentHandler) RemoveGame(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID турнира"})
		return
	}
	gameID, err := strconv.Atoi(c.Param("game_id"))
	if err != nil || gameID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}
	userID := c.GetUint("userID")

	if err := h.tournamentService.RemoveGame(c.Request.Context(), uint(id), uint(gameID), userID); err != nil {
		log.Error().Err(err).Int("tournament_id", id).Int("game_id", gameID).Uint("user_id", userID).Msg("TournamentHandler.RemoveGame: failed to remove game")
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, "/tournaments/"+strconv.Itoa(id)+"/games")
}

// ApplyForm отображает форму подачи заявки на турнир.
func (h *TournamentHandler) ApplyForm(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID турнира"})
		return
	}
	userID := c.GetUint("userID")

	teams, err := h.teamService.GetMyTeams(c.Request.Context(), userID)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("TournamentHandler.ApplyForm: failed to get teams")
		teams = []team.Team{}
	}

	render.Page(c, http.StatusOK, "tournaments-apply.html", gin.H{
		"TournamentID": id,
		"Teams":        teams,
		"csrf":         csrf.GetToken(c),
	})
}

// Apply подаёт заявку на турнир.
func (h *TournamentHandler) Apply(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID турнира"})
		return
	}
	userID := c.GetUint("userID")

	var input ApplyInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверные данные: " + err.Error()})
		return
	}

	if err := h.tournamentService.Apply(c.Request.Context(), uint(id), input.TeamID, userID); err != nil {
		log.Error().Err(err).Int("tournament_id", id).Uint("team_id", input.TeamID).Uint("user_id", userID).Msg("TournamentHandler.Apply: failed to apply")
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, "/tournaments/"+strconv.Itoa(id))
}
