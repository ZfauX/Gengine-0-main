// internal/domain/social/handler.go
package social

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	csrf "github.com/utrack/gin-csrf"
)

// FollowHandler обрабатывает запросы подписок.
type FollowHandler struct {
	followService *FollowService
}

func NewFollowHandler(followService *FollowService) *FollowHandler {
	return &FollowHandler{followService: followService}
}

// Follow подписывает текущего пользователя на автора.
// @Summary Подписаться на автора
// @Description Создаёт подписку текущего пользователя на автора игр
// @Tags social
// @Accept json
// @Produce json
// @Param id path int true "ID автора"
// @Success 200 {object} map[string]interface{} "Статус followed"
// @Failure 400 {object} map[string]interface{} "Неверный ID автора"
// @Router /follow/{id} [post]
// @Security JWT
func (h *FollowHandler) Follow(c *gin.Context) {
	authorID, err := strconv.Atoi(c.Param("id"))
	if err != nil || authorID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "некорректный ID автора"})
		return
	}
	userID := c.GetUint("userID")

	if err := h.followService.Follow(c.Request.Context(), userID, uint(authorID)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "followed"})
}

// Unfollow отменяет подписку.
// @Summary Отписаться от автора
// @Description Удаляет подписку текущего пользователя на автора
// @Tags social
// @Accept json
// @Produce json
// @Param id path int true "ID автора"
// @Success 200 {object} map[string]interface{} "Статус unfollowed"
// @Failure 400 {object} map[string]interface{} "Неверный ID автора"
// @Router /follow/{id} [delete]
// @Security JWT
func (h *FollowHandler) Unfollow(c *gin.Context) {
	authorID, err := strconv.Atoi(c.Param("id"))
	if err != nil || authorID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "некорректный ID автора"})
		return
	}
	userID := c.GetUint("userID")

	if err := h.followService.Unfollow(c.Request.Context(), userID, uint(authorID)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "unfollowed"})
}

// IsFollowing проверяет статус подписки.
// @Summary Проверить подписку
// @Description Проверяет, подписан ли текущий пользователь на автора
// @Tags social
// @Produce json
// @Param id path int true "ID автора"
// @Success 200 {object} map[string]interface{} "Статус подписки"
// @Failure 400 {object} map[string]interface{} "Неверный ID автора"
// @Router /follow/{id}/check [get]
// @Security JWT
func (h *FollowHandler) IsFollowing(c *gin.Context) {
	authorID, err := strconv.Atoi(c.Param("id"))
	if err != nil || authorID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "некорректный ID автора"})
		return
	}
	userID := c.GetUint("userID")

	following := h.followService.IsFollowing(c.Request.Context(), userID, uint(authorID))
	c.JSON(http.StatusOK, gin.H{"following": following})
}

// Subscriptions отображает список подписок текущего пользователя.
// @Summary Список подписок
// @Description Отображает HTML-страницу с авторами, на которых подписан текущий пользователь
// @Tags social
// @Produce html
// @Success 200 {string} html "Страница подписок"
// @Router /subscriptions [get]
// @Security JWT
func (h *FollowHandler) Subscriptions(c *gin.Context) {
	userID := c.GetUint("userID")
	authors, err := h.followService.GetSubscriptions(c.Request.Context(), userID)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "follow-list.html",
		"Authors":      authors,
		"csrf":         csrf.GetToken(c),
	})
}
