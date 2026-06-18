// internal/domain/tournament/handler.go
package tournament

import (
	"net/http"
	"strconv"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/team"

	"github.com/utrack/gin-csrf"
	"github.com/gin-gonic/gin"
)

// ---------- Входные структуры для валидации ----------

type AddGameInput struct {
	GameID uint `form:"game_id" binding:"required,gt=0"`
}

type ApplyInput struct {
	TeamID uint `form:"team_id" binding:"required,gt=0"`
}

// ---------- Обработчики ----------

// TournamentHandler обрабатывает запросы, связанные с турнирами.
type TournamentHandler struct {
	tournamentService *TournamentService
	teamService       *team.TeamService
	gameService       *game.GameService
}

// NewTournamentHandler создаёт новый TournamentHandler.
func NewTournamentHandler(
	tournamentSvc *TournamentService,
	teamSvc *team.TeamService,
	gameSvc *game.GameService,
) *TournamentHandler {
	return &TournamentHandler{
		tournamentService: tournamentSvc,
		teamService:       teamSvc,
		gameService:       gameSvc,
	}
}

// ---------- Турниры ----------

// ListTournaments отображает список всех турниров.
func (h *TournamentHandler) ListTournaments(c *gin.Context) {
	tournaments, err := h.tournamentService.List()
	if err != nil {
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "tournaments-list.html",
		"Tournaments":  tournaments,
	})
}

// NewTournamentForm отображает форму создания турнира.
func (h *TournamentHandler) NewTournamentForm(c *gin.Context) {
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "tournaments-new.html",
		"csrf":         csrf.GetToken(c),
	})
}

// CreateTournament создаёт новый турнир.
func (h *TournamentHandler) CreateTournament(c *gin.Context) {
	userID := c.GetUint("userID")
	var tournament Tournament
	if err := c.ShouldBind(&tournament); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "tournaments-new.html",
			"Error":        "Неверные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}
	tournament.AuthorID = userID

	if err := h.tournamentService.Create(&tournament); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "tournaments-new.html",
			"Error":        err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}
	c.Redirect(http.StatusFound, "/tournaments")
}

// ShowTournament отображает страницу турнира с играми, участниками и результатами.
func (h *TournamentHandler) ShowTournament(c *gin.Context) {
	tournamentID, _ := strconv.Atoi(c.Param("id"))

	tournament, err := h.tournamentService.GetByID(uint(tournamentID))
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}

	// Список игр турнира
	games, _ := h.tournamentService.ListGames(uint(tournamentID))

	// Турнирная таблица
	results, _ := h.tournamentService.GetLeaderboard(uint(tournamentID))

	// Проверяем, может ли текущий пользователь подать заявку
	userID := c.GetUint("userID")
	canApply := h.tournamentService.CanApply(uint(tournamentID), userID)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "tournaments-show.html",
		"Tournament":   tournament,
		"Games":        games,
		"Results":      results,
		"CanApply":     canApply,
	})
}

// EditTournamentForm отображает форму редактирования турнира.
func (h *TournamentHandler) EditTournamentForm(c *gin.Context) {
	tournamentID, _ := strconv.Atoi(c.Param("id"))
	tournament, err := h.tournamentService.GetByID(uint(tournamentID))
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "tournaments-edit.html",
		"Tournament":   tournament,
		"csrf":         csrf.GetToken(c),
	})
}

// UpdateTournament обновляет турнир.
func (h *TournamentHandler) UpdateTournament(c *gin.Context) {
	tournamentID, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	var updated Tournament
	if err := c.ShouldBind(&updated); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "tournaments-edit.html",
			"Error":        "Неверные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	if err := h.tournamentService.Update(uint(tournamentID), &updated, userID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/tournaments/"+c.Param("id"))
}

// ---------- Игры турнира ----------

// AddGameForm отображает форму добавления игры в турнир.
func (h *TournamentHandler) AddGameForm(c *gin.Context) {
	tournamentID, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	// Получаем игры автора, которых ещё нет в турнире
	games, err := h.tournamentService.GetAvailableGames(uint(tournamentID), userID)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "tournaments-add.html",
		"TournamentID": tournamentID,
		"Games":        games,
		"csrf":         csrf.GetToken(c),
	})
}

// AddGame добавляет существующую игру в турнир.
func (h *TournamentHandler) AddGame(c *gin.Context) {
	tournamentID, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	var input AddGameInput
	if err := c.ShouldBind(&input); err != nil {
		games, _ := h.tournamentService.GetAvailableGames(uint(tournamentID), userID)
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "tournaments-add.html",
			"Error":        "Неверные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
			"Games":        games,
		})
		return
	}

	if err := h.tournamentService.AddGame(uint(tournamentID), input.GameID, userID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/tournaments/"+c.Param("id"))
}

// RemoveGame удаляет игру из турнира.
func (h *TournamentHandler) RemoveGame(c *gin.Context) {
	tournamentID, _ := strconv.Atoi(c.Param("id"))
	gameID, _ := strconv.Atoi(c.Param("game_id"))
	userID := c.GetUint("userID")

	if err := h.tournamentService.RemoveGame(uint(tournamentID), uint(gameID), userID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/tournaments/"+c.Param("id"))
}

// ---------- Заявки на турнир ----------

// Apply подаёт заявку команды на участие в турнире.
func (h *TournamentHandler) Apply(c *gin.Context) {
	tournamentID, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	var input ApplyInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "tournaments-apply.html",
			"Error":        "Неверные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	if err := h.tournamentService.Apply(uint(tournamentID), input.TeamID, userID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/tournaments/"+c.Param("id"))
}