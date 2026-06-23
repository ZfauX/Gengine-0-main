// internal/domain/game/review_handler.go
package game

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// ReviewHandler обрабатывает отзывы.
type ReviewHandler struct {
	reviewService *ReviewService
}

func NewReviewHandler(reviewService *ReviewService) *ReviewHandler {
	return &ReviewHandler{reviewService: reviewService}
}

// ShowForm отображает форму создания отзыва, если пользователь имеет право.
func (h *ReviewHandler) ShowForm(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("game_id"))
	userID := c.GetUint("userID")

	can, err := h.reviewService.CanReview(uint(gameID), userID)
	if err != nil || !can {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": "Вы не можете оставить отзыв"})
		return
	}

	c.HTML(http.StatusOK, "reviews/new.html", gin.H{
		"GameID": gameID,
	})
}

// Create сохраняет новый отзыв.
func (h *ReviewHandler) Create(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("game_id"))
	userID := c.GetUint("userID")
	rating, err := strconv.Atoi(c.PostForm("rating"))
	if err != nil {
		c.HTML(http.StatusOK, "reviews/new.html", gin.H{
			"GameID": gameID,
			"Error":  "Неверный рейтинг",
		})
		return
	}
	comment := c.PostForm("comment")

	if err := h.reviewService.Create(uint(gameID), userID, rating, comment); err != nil {
		c.HTML(http.StatusOK, "reviews/new.html", gin.H{
			"GameID": gameID,
			"Error":  err.Error(),
		})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("game_id"))
}