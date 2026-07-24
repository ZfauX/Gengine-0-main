// internal/domain/game/rating_handler.go
package game

import (
	"net/http"

	"gengine-0/internal/pkg/render"

	"github.com/gin-gonic/gin"
)

// RatingHandler обрабатывает рейтинг игроков.
type RatingHandler struct {
	ratingService *RatingService
}

func NewRatingHandler(ratingService *RatingService) *RatingHandler {
	return &RatingHandler{ratingService: ratingService}
}

// Leaderboard отображает таблицу лидеров.
func (h *RatingHandler) Leaderboard(c *gin.Context) {
	leaderboard, err := h.ratingService.GetLeaderboard(c.Request.Context(), 50)
	if err != nil {
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	type templateEntry struct {
		Place      int
		UserID     uint
		Score      int
		Name       string
		AvatarPath string
	}
	var entries []templateEntry
	for i, r := range leaderboard {
		entries = append(entries, templateEntry{
			Place:      i + 1,
			UserID:     r.UserID,
			Score:      r.Score,
			Name:       r.Name,
			AvatarPath: r.AvatarPath,
		})
	}

	render.Page(c, http.StatusOK, "ratings-leaderboard.html", gin.H{
		"Entries": entries,
	})
}
