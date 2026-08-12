// internal/domain/game/fullpreview_handler.go
package game

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"gengine-0/internal/domain/level"
	apperr "gengine-0/internal/pkg/errors"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// FullPreviewHandler обрабатывает запросы полного просмотра игры.
type FullPreviewHandler struct {
	gameService  GameServiceInterface
	levelService *level.LevelService

	// previewCache (P-H2, PASS-8): короткий TTL-кэш (10с) списка уровней с
	// вопросами/ответами для предпросмотра. ListWithQuestions грузит весь граф
	// (Levels→Questions→Answers) — тысячи строк на запрос; автор/соавтор часто
	// открывает предпросмотр повторно при редактировании. Устаревание ≤10с
	// приемлемо (данные меняются редко, при сохранении правок).
	previewMu    sync.Mutex
	previewCache map[uint]previewCacheEntry
}

type previewCacheEntry struct {
	levels  []level.Level
	expires time.Time
}

// previewCacheTTL — время жизни кэша предпросмотра.
const previewCacheTTL = 10 * time.Second

// NewFullPreviewHandler создаёт новый FullPreviewHandler.
func NewFullPreviewHandler(
	gameService GameServiceInterface,
	levelService *level.LevelService,
) *FullPreviewHandler {
	return &FullPreviewHandler{
		gameService:  gameService,
		levelService: levelService,
		previewCache: make(map[uint]previewCacheEntry),
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
// @Failure 403 {object} map[string]interface{} "handler.forbidden"
// @Router /games/{id}/full-preview [get]
// @Security JWT
func (h *FullPreviewHandler) FullPreview(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": render.Tr(c, "handler.invalid_game_id"),
			"code":  "bad_request",
		})
		return
	}
	userID := c.GetUint("userID")

	_, err = h.gameService.GetByID(c.Request.Context(), uint(gameID), userID, middleware.IsAdmin(c))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
				"error": render.Tr(c, "handler.game_not_found"),
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

	// K-1 (pass 36): коды ответов доступны только автору/соавтору (менеджеру).
	// Раньше полный предпросмотр отдавал Answer.Code любому аутентифицированному
	// пользователю для публичных игр — ломало честность игры (ответы до старта).
	isManager, mgrErr := h.gameService.IsUserManager(c.Request.Context(), uint(gameID), userID)
	if mgrErr != nil {
		log.Error().Err(mgrErr).Int("game_id", gameID).Msg("FullPreview: failed to check manager rights")
		appErr := apperr.Wrap(mgrErr, "FullPreview: failed to check manager rights")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	levels, err := h.getPreviewLevels(c.Request.Context(), uint(gameID))
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
			// K-1 (pass 36) + S-1 (pass 37): менеджеры видят ответы И подсказки
			// (предпросмотр при редактировании). Остальные — только текст вопроса:
			// подсказка выдаётся в геймплее по кнопке UseHint со штрафом, её утечка
			// до старта разрушает экономику игры.
			qp := questionPreview{Text: q.Text}
			if isManager {
				qp.Hint = q.Hint
				for _, a := range q.Answers {
					qp.Answers = append(qp.Answers, a.Code)
				}
			}
			lp.Questions = append(lp.Questions, qp)
		}
		result = append(result, lp)
	}

	c.JSON(http.StatusOK, gin.H{"data": result})
}

// getPreviewLevels (P-H2, PASS-8): загрузка уровней с вопросами/ответами
// для предпросмотра с коротким TTL-кэшем. Сырые уровни (включая коды ответов)
// живут только в памяти сервера; клиенту коды уходят только при isManager
// (фильтрация ниже в FullPreview).
func (h *FullPreviewHandler) getPreviewLevels(ctx context.Context, gameID uint) ([]level.Level, error) {
	now := time.Now()
	h.previewMu.Lock()
	if e, ok := h.previewCache[gameID]; ok && now.Before(e.expires) {
		h.previewMu.Unlock()
		return e.levels, nil
	}
	h.previewMu.Unlock()

	levels, err := h.levelService.ListWithQuestions(ctx, gameID)
	if err != nil {
		return nil, err
	}

	h.previewMu.Lock()
	h.previewCache[gameID] = previewCacheEntry{levels: levels, expires: now.Add(previewCacheTTL)}
	h.previewMu.Unlock()
	return levels, nil
}
