// internal/domain/game/rating_handler.go
package game

import (
	"net/http"

	"gengine-0/internal/domain/user"

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
	ratings, err := h.ratingService.GetLeaderboard(50)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}

	type leaderboardEntry struct {
		Place int
		User  user.User
		Score int
	}
	var entries []leaderboardEntry
	for i, r := range ratings {
		entries = append(entries, leaderboardEntry{
			Place: i + 1,
			User:  r.User,
			Score: r.Score,
		})
	}

	c.HTML(http.StatusOK, "ratings/leaderboard.html", gin.H{
		"Entries": entries,
	})
}