// internal/domain/game/simulate_handler.go
package game

import (
	"net/http"
	"strconv"

	"gengine-0/internal/pkg/render"

	"github.com/gin-gonic/gin"
)

// SimulateHandler обрабатывает симуляцию прохождения игр.
type SimulateHandler struct {
	simulateService *SimulateService
}

// NewSimulateHandler создаёт новый SimulateHandler.
func NewSimulateHandler(simulateService *SimulateService) *SimulateHandler {
	return &SimulateHandler{simulateService: simulateService}
}

// Simulate запускает симуляцию прохождения игры.
// Simulate отображает страницу симуляции прохождения.
// @Summary Симуляция прохождения
// @Tags games
// @Produce html
// @Param id path int true "ID игры"
// @Success 200 {string} html "Страница симуляции"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Router /games/{id}/simulate [get]
// @Security JWT
func (h *SimulateHandler) Simulate(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID := c.GetUint("userID")
	result, err := h.simulateService.Simulate(c.Request.Context(), uint(gameID), userID)
	if err != nil {
		render.RenderError(c, http.StatusForbidden, err.Error())
		return
	}
	render.Page(c, http.StatusOK, "simulate-results.html", gin.H{
		"GameID": gameID,
		"Result": result,
	})
}
