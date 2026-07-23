// internal/domain/game/fullpreview_handler.go
package game

import (
	"errors"
	"net/http"
	"strconv"

	"gengine-0/internal/domain/level"
	apperr "gengine-0/internal/pkg/errors"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// FullPreviewHandler обрабатывает запросы полного просмотра игры.
type FullPreviewHandler struct {
	gameService  GameServiceInterface
	levelService *level.LevelService
}

// NewFullPreviewHandler создаёт новый FullPreviewHandler.
func NewFullPreviewHandler(
	gameService GameServiceInterface,
	levelService *level.LevelService,
) *FullPreviewHandler {
	return &FullPreviewHandler{
		gameService:  gameService,
		levelService: levelService,
	}
}

// FullPreview возвращает полную структуру игры для быстрого просмотра.
// FullPreview возвращает полную структуру игры в JSON-формате.
// @Summary Полный просмотр игры (JSON)
// @Tags games
// @Produce json
// @Param id path int true "ID игры"
// @Success 200 {object} map[string]interface{} "Полная структура игры"
// @Failure 400 {object} map[string]interface{} "Неверный ID"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Router /games/{id}/full-preview [get]
// @Security JWT
func (h *FullPreviewHandler) FullPreview(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Неверный ID игры",
			"code":  "bad_request",
		})
		return
	}
	userID := c.GetUint("userID")

	_, err = h.gameService.GetByID(c.Request.Context(), uint(gameID), userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
				"error": "Игра не найдена",
				"code":  "not_found",
			})
		} else {
			appErr := apperr.Forbidden("Нет доступа к игре")
			c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
				"error": appErr.Message,
				"code":  appErr.Code,
			})
		}
		return
	}

	levels, err := h.levelService.ListWithQuestions(c.Request.Context(), uint(gameID))
	if err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("FullPreview: failed to load levels")
		appErr := apperr.Wrap(err, "FullPreview: failed to load levels")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	var result []levelPreview
	for _, lvl := range levels {
		lp := levelPreview{
			ID:          lvl.ID,
			Position:    lvl.Position,
			Name:        lvl.Name,
			Description: lvl.Description,
		}
		for _, q := range lvl.Questions {
			qp := questionPreview{Text: q.Text, Hint: q.Hint}
			for _, a := range q.Answers {
				qp.Answers = append(qp.Answers, a.Code)
			}
			lp.Questions = append(lp.Questions, qp)
		}
		result = append(result, lp)
	}

	c.JSON(http.StatusOK, gin.H{"data": result})
}
