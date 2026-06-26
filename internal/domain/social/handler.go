// internal/domain/social/handler.go
package social

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
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
func (h *FollowHandler) Follow(c *gin.Context) {
	authorID, err := strconv.Atoi(c.Param("id"))
	if err != nil || authorID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "некорректный ID автора"})
		return
	}
	userID := c.GetUint("userID")
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "требуется аутентификация"})
		return
	}

	if err := h.followService.Follow(c.Request.Context(), userID, uint(authorID)); err != nil {
		log.Error().Err(err).Uint("user_id", userID).Int("author_id", authorID).Msg("Follow: failed to follow author")
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
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "требуется аутентификация"})
		return
	}

	if err := h.followService.Unfollow(c.Request.Context(), userID, uint(authorID)); err != nil {
		log.Error().Err(err).Uint("user_id", userID).Int("author_id", authorID).Msg("Unfollow: failed to unfollow author")
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
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "требуется аутентификация"})
		return
	}

	following := h.followService.IsFollowing(c.Request.Context(), userID, uint(authorID))
	c.JSON(http.StatusOK, gin.H{"following": following})
}

// Subscriptions отображает список подписок текущего пользователя.
func (h *FollowHandler) Subscriptions(c *gin.Context) {
	userID := c.GetUint("userID")
	if userID == 0 {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}

	authors, err := h.followService.GetSubscriptions(c.Request.Context(), userID)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("Subscriptions: failed to get subscriptions")
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "follow-list.html",
		"Authors":      authors,
		"csrf":         csrf.GetToken(c),
	})
}
