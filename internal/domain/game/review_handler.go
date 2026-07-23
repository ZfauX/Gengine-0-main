// internal/domain/game/review_handler.go
package game

import (
	"errors"
	"net/http"
	"strconv"

	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/validation"

	"github.com/gin-gonic/gin"
)

const (
	maxRating              = 5
	maxReviewCommentLength = 500
)

// ReviewHandler обрабатывает отзывы.
type ReviewHandler struct {
	reviewService *ReviewService
}

func NewReviewHandler(reviewService *ReviewService) *ReviewHandler {
	return &ReviewHandler{reviewService: reviewService}
}

// ShowForm отображает форму создания отзыва, если пользователь имеет право.
// ShowForm отображает форму отзыва об игре.
// @Summary Форма для отзыва
// @Tags games
// @Produce html
// @Param id path int true "ID игры"
// @Success 200 {string} html "Форма отзыва"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Router /games/{id}/review [get]
// @Security JWT
func (h *ReviewHandler) ShowForm(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("game_id"))
	userID := c.GetUint("userID")

	can, err := h.reviewService.CanReview(uint(gameID), userID)
	if err != nil || !can {
		render.RenderError(c, http.StatusForbidden, "Вы не можете оставить отзыв")
		return
	}

	render.Page(c, http.StatusOK, "reviews-new.html", gin.H{
		"GameID": gameID,
	})
}

// Create сохраняет новый отзыв.
// Create отправляет отзыв об игре.
// @Summary Отправка отзыва
// @Tags games
// @Accept x-www-form-urlencoded
// @Produce html
// @Param id path int true "ID игры"
// @Param rating formData int true "Оценка (1-5)"
// @Param text formData string true "Текст отзыва"
// @Success 302 {string} string "Перенаправление на /games/{id}"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Router /games/{id}/review [post]
// @Security JWT
func (h *ReviewHandler) Create(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("game_id"))
	userID := c.GetUint("userID")

	errs := validation.FieldErrors{}
	rating, err := strconv.Atoi(c.PostForm("rating"))
	if err != nil || rating < 1 || rating > maxRating {
		errs.Add("rating", errors.New("рейтинг должен быть от 1 до 5"))
	}
	comment := c.PostForm("comment")
	if len(comment) > maxReviewCommentLength {
		errs.Add("comment", errors.New("комментарий не может превышать 500 символов"))
	}
	if errs.HasErrors() {
		render.Page(c, http.StatusOK, "reviews-new.html", gin.H{
			"GameID": gameID,
			"Error":  errs.Error(),
			"Errors": errs,
		})
		return
	}

	if err := h.reviewService.Create(uint(gameID), userID, rating, comment); err != nil {
		render.Page(c, http.StatusOK, "reviews-new.html", gin.H{
			"GameID": gameID,
			"Error":  err.Error(),
		})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("game_id"))
}
