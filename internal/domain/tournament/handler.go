// internal/domain/tournament/handler.go
package tournament

import (
	"net/http"
	"strconv"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/team"

	"github.com/gin-gonic/gin"
	csrf "github.com/utrack/gin-csrf"
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
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "tournaments-list.html",
		"Tournaments":  tournaments,
	})
}

// Show отображает один турнир с таблицей лидеров.
func (h *TournamentHandler) Show(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	t, err := h.tournamentService.GetByID(c.Request.Context(), uint(id))
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}

	games, _ := h.tournamentService.ListGames(c.Request.Context(), uint(id))
	leaderboard, _ := h.tournamentService.GetLeaderboard(c.Request.Context(), uint(id))
	canApply := h.tournamentService.CanApply(c.Request.Context(), uint(id), userID)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock":  "tournaments-show.html",
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
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "tournaments-new.html",
		"csrf":         csrf.GetToken(c),
	})
}

// Create создаёт новый турнир.
func (h *TournamentHandler) Create(c *gin.Context) {
	userID := c.GetUint("userID")

	var input CreateTournamentInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "tournaments-new.html",
			"Error":        "Неверные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
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
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "tournaments-new.html",
			"Error":        err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	c.Redirect(http.StatusFound, "/tournaments/"+strconv.Itoa(int(t.ID)))
}

// EditForm отображает форму редактирования турнира.
func (h *TournamentHandler) EditForm(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	t, err := h.tournamentService.GetByID(c.Request.Context(), uint(id))
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}
	if t.AuthorID != userID {
		c.HTML(http.StatusForbidden, "errors/403.html", nil)
		return
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "tournaments-edit.html",
		"Tournament":   t,
		"csrf":         csrf.GetToken(c),
	})
}

// Update обновляет турнир.
func (h *TournamentHandler) Update(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	var input UpdateTournamentInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "tournaments-edit.html",
			"Error":        "Неверные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
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
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "tournaments-edit.html",
			"Error":        err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	c.Redirect(http.StatusFound, "/tournaments/"+strconv.Itoa(id))
}

// Games отображает список игр турнира.
func (h *TournamentHandler) Games(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	games, err := h.tournamentService.ListGames(c.Request.Context(), uint(id))
	if err != nil {
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}

	availableGames, _ := h.tournamentService.GetAvailableGames(c.Request.Context(), uint(id), userID)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock":   "tournaments-games.html",
		"TournamentID":   id,
		"Games":          games,
		"AvailableGames": availableGames,
		"csrf":           csrf.GetToken(c),
	})
}

// AddGame добавляет игру в турнир.
func (h *TournamentHandler) AddGame(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	var input AddGameInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": "Неверные данные: " + err.Error()})
		return
	}

	if err := h.tournamentService.AddGame(c.Request.Context(), uint(id), input.GameID, userID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, "/tournaments/"+strconv.Itoa(id)+"/games")
}

// RemoveGame удаляет игру из турнира.
func (h *TournamentHandler) RemoveGame(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	gameID, _ := strconv.Atoi(c.Param("game_id"))
	userID := c.GetUint("userID")

	if err := h.tournamentService.RemoveGame(c.Request.Context(), uint(id), uint(gameID), userID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, "/tournaments/"+strconv.Itoa(id)+"/games")
}

// ApplyForm отображает форму подачи заявки на турнир.
func (h *TournamentHandler) ApplyForm(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	teams, _ := h.teamService.GetMyTeams(c.Request.Context(), userID)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "tournaments-apply.html",
		"TournamentID": id,
		"Teams":        teams,
		"csrf":         csrf.GetToken(c),
	})
}

// Apply подаёт заявку на турнир.
func (h *TournamentHandler) Apply(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	var input ApplyInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": "Неверные данные: " + err.Error()})
		return
	}

	if err := h.tournamentService.Apply(c.Request.Context(), uint(id), input.TeamID, userID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, "/tournaments/"+strconv.Itoa(id))
}
