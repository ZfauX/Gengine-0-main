// internal/domain/game/autocomplete_handler.go
package game

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"

	"gengine-0/internal/pkg/sqlutil"
)

const (
	maxQueryLength = 100
	minQueryLength = 2
	maxResults     = 10
)

// AutocompleteInput — входные параметры для автодополнения поиска
type AutocompleteInput struct {
	Q string `form:"q" binding:"omitempty,max=100"` // maxQueryLength
}

// AutocompleteHandler обрабатывает запросы автодополнения поиска игр
type AutocompleteHandler struct {
	db *gorm.DB
}

// NewAutocompleteHandler создаёт обработчик автодополнения
func NewAutocompleteHandler(db *gorm.DB) *AutocompleteHandler {
	return &AutocompleteHandler{db: db}
}

// Games возвращает список названий игр, совпадающих с запросом
func (h *AutocompleteHandler) Games(c *gin.Context) {
	input := AutocompleteInput{}
	if err := c.ShouldBindQuery(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "неверный запрос"})
		return
	}

	q := input.Q
	if len(q) < minQueryLength {
		c.JSON(http.StatusOK, gin.H{"results": []map[string]any{}})
		return
	}

	// Ищем игры по названию (full-text + ILIKE fallback) — максимум 10 результатов
	var results []map[string]any
	err := h.db.Table("games").
		Select("id, name").
		Where("is_draft = false AND visibility = 'public' AND (search_vector @@ plainto_tsquery('russian', ?) OR name ILIKE ?)", q, "%"+sqlutil.EscapeLike(q)+"%").
		Limit(maxResults).
		Find(&results).Error

	if err != nil {
		log.Error().Err(err).Str("query", q).Msg("Autocomplete: failed to search games")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "ошибка поиска"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"results": results})
}

// GameStatsHandler возвращает статистику игры через API
type GameStatsHandler struct {
	gameService     GameServiceInterface
	gamePlayService *GamePlayService
}

// NewGameStatsHandler создаёт обработчик статистики
func NewGameStatsHandler(gs GameServiceInterface, gps *GamePlayService) *GameStatsHandler {
	return &GameStatsHandler{gameService: gs, gamePlayService: gps}
}

// Show возвращает JSON-статистику игры
func (h *GameStatsHandler) Show(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "неверный ID игры"})
		return
	}

	userID := c.GetUint("userID")
	game, err := h.gameService.GetByID(c, uint(id), userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "игра не найдена"})
		return
	}

	reviews, err := h.gameService.ListReviews(c, uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "ошибка загрузки отзывов"})
		return
	}
	avgRating, reviewsCount, err := h.gameService.GetAverageRating(c, uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "ошибка расчёта рейтинга"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":            game.ID,
		"name":          game.Name,
		"description":   game.Description,
		"author_id":     game.AuthorID,
		"author_name":   game.Author.Name,
		"is_draft":      game.IsDraft,
		"visibility":    game.Visibility,
		"starts_at":     game.StartsAt,
		"rating":        avgRating,
		"reviews_count": reviewsCount,
		"reviews":       reviews,
		"cover_path":    game.CoverPath,
		"created_at":    game.CreatedAt,
		"updated_at":    game.UpdatedAt,
	})
}
