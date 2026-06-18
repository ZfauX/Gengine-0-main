// internal/domain/social/handler.go
package social

import (
	"net/http"
	"strconv"

	"github.com/utrack/gin-csrf"
	"github.com/gin-gonic/gin"
)

// FollowHandler обрабатывает запросы подписок.
type FollowHandler struct {
	followService *FollowService
}

func NewFollowHandler(followService *FollowService) *FollowHandler {
	return &FollowHandler{followService: followService}
}

// Follow подписывает текущего пользователя на автора.
func (h *FollowHandler) Follow(c *gin.Context) {
	authorID, err := strconv.Atoi(c.Param("id"))
	if err != nil || authorID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "некорректный ID автора"})
		return
	}
	userID := c.GetUint("userID")

	if err := h.followService.Follow(userID, uint(authorID)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "followed"})
}

// Unfollow отменяет подписку.
func (h *FollowHandler) Unfollow(c *gin.Context) {
	authorID, err := strconv.Atoi(c.Param("id"))
	if err != nil || authorID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "некорректный ID автора"})
		return
	}
	userID := c.GetUint("userID")

	if err := h.followService.Unfollow(userID, uint(authorID)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "unfollowed"})
}

// IsFollowing проверяет статус подписки.
func (h *FollowHandler) IsFollowing(c *gin.Context) {
	authorID, err := strconv.Atoi(c.Param("id"))
	if err != nil || authorID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "некорректный ID автора"})
		return
	}
	userID := c.GetUint("userID")

	following := h.followService.IsFollowing(userID, uint(authorID))
	c.JSON(http.StatusOK, gin.H{"following": following})
}

// Subscriptions отображает список подписок текущего пользователя.
func (h *FollowHandler) Subscriptions(c *gin.Context) {
	userID := c.GetUint("userID")
	authors, err := h.followService.GetSubscriptions(userID)
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