// internal/domain/game/hnd_geolocation.go
// G-1..G-4 (pass 45): API геолокации игроков.
package game

import (
	"math"
	"net/http"
	"strconv"

	"gengine-0/internal/pkg/render"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// GeolocationHandler — HTTP-обработчики геолокации.
type GeolocationHandler struct {
	geoService  *GeolocationService
	passingRepo GamePassingRepository
	gameRepo    GameRepository
}

func NewGeolocationHandler(geoService *GeolocationService, passingRepo GamePassingRepository, gameRepo GameRepository) *GeolocationHandler {
	return &GeolocationHandler{geoService: geoService, passingRepo: passingRepo, gameRepo: gameRepo}
}

// UpdateLocation сохраняет позицию игрока (G-2).
// @Summary Обновление позиции игрока
// @Tags geolocation
// @Accept json
// @Param id path int true "ID прохождения"
// @Param body body map[string]float64 true "lat, lng, accuracy"
// @Success 204 {string} string "OK"
// @Security JWT
func (h *GeolocationHandler) UpdateLocation(c *gin.Context) {
	passingID, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")
	if passingID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid passing id"})
		return
	}
	var input struct {
		Lat      float64 `json:"lat"`
		Lng      float64 `json:"lng"`
		Accuracy float64 `json:"accuracy"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}
	// Валидация координат. L4 (PASS-7): NaN/Inf проходят обычные сравнения —
	// math.IsNaN/IsInf отклоняют их (как в payment handler PASS-4 M3).
	if math.IsNaN(input.Lat) || math.IsInf(input.Lat, 0) ||
		math.IsNaN(input.Lng) || math.IsInf(input.Lng, 0) ||
		math.IsNaN(input.Accuracy) || math.IsInf(input.Accuracy, 0) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid coordinates"})
		return
	}
	if input.Lat < -90 || input.Lat > 90 || input.Lng < -180 || input.Lng > 180 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid coordinates"})
		return
	}
	// DEEP-REVIEW HIGH #10 (pass 46): Accuracy должна быть разумной
	// (метры). Отрицательные или абсурдные значения отклоняем.
	if input.Accuracy < 0 || input.Accuracy > 10000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid accuracy"})
		return
	}

	// Членство: пользователь должен быть в команде прохождения.
	passing, err := h.passingRepo.GetByID(c.Request.Context(), uint(passingID))
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": render.Tr(c, "handler.forbidden")})
		return
	}
	// DEEP-REVIEW HIGH #10 (pass 46): позицию можно слать только пока игра
	// активна (started/testing). Завершённые/дисквалифицированные прохождения
	// больше не принимают GPS (раньше участник мог слать координаты вечно).
	if passing.Status != StatusStarted && passing.Status != StatusTesting {
		c.JSON(http.StatusForbidden, gin.H{"error": render.Tr(c, "handler.forbidden")})
		return
	}
	member, mErr := h.gameRepo.IsTeamMember(c.Request.Context(), passing.TeamID, userID)
	if mErr != nil || !member {
		c.JSON(http.StatusForbidden, gin.H{"error": render.Tr(c, "handler.forbidden")})
		return
	}

	if err := h.geoService.UpdateLocation(c.Request.Context(), uint(passingID), passing.TeamID, userID, input.Lat, input.Lng, input.Accuracy); err != nil {
		log.Error().Err(err).Int("passing_id", passingID).Uint("user_id", userID).Msg("UpdateLocation: failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save location"})
		return
	}
	c.Status(http.StatusNoContent)
}

// LocationsByGame возвращает позиции игроков игры для мониторинга (G-3).
// @Summary Позиции игроков игры
// @Tags geolocation
// @Param id path int true "ID игры"
// @Success 200 {object} map[string]any
// @Security JWT
func (h *GeolocationHandler) LocationsByGame(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))
	if gameID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid game id"})
		return
	}
	locs, err := h.geoService.LocationsByGame(c.Request.Context(), uint(gameID))
	if err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("LocationsByGame: failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load locations"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"locations": locs})
}
