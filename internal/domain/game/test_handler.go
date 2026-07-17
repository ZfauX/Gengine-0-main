// internal/domain/game/test_handler.go
package game

import (
	"errors"
	"net/http"
	"strconv"

	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	csrf "github.com/utrack/gin-csrf"
	"gorm.io/gorm"
)

// TestHandler обрабатывает тестовые прохождения игр.
type TestHandler struct {
	gameService    GameServiceInterface
	passingService GamePassingServiceInterface
}

// NewTestHandler создаёт новый TestHandler.
func NewTestHandler(
	gameService GameServiceInterface,
	passingService GamePassingServiceInterface,
) *TestHandler {
	return &TestHandler{
		gameService:    gameService,
		passingService: passingService,
	}
}

// TestPage отображает страницу управления тестовыми прохождениями.
func (h *TestHandler) TestPage(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID := c.GetUint("userID")

	g, err := h.gameService.GetByID(c.Request.Context(), uint(gameID), userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			render.RenderErrorPage(c, http.StatusNotFound)
		} else {
			log.Error().Err(err).Int("game_id", gameID).Msg("GameHandler.TestPage: failed to get game")
			render.RenderErrorPage(c, http.StatusInternalServerError)
		}
		return
	}

	var testPassings []GamePassing
	if err := h.passingService.ListTestPassings(c.Request.Context(), g.ID, &testPassings); err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("GameHandler.TestPage: failed to list test passings")
	}

	isAdmin := middleware.IsAdmin(c)

	render.Page(c, http.StatusOK, "games-test.html", gin.H{
		"Game":          g,
		"TestPassings":  testPassings,
		"csrf":          csrf.GetToken(c),
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
	})
}
