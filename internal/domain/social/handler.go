// internal/domain/social/handler.go
package social

import (
	"net/http"

	apperrors "gengine-0/internal/pkg/errors"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	csrf "github.com/utrack/gin-csrf"
)

// ---------- Входные структуры для валидации ----------

// AuthorIDRequest используется для валидации ID автора в URL.
type AuthorIDRequest struct {
	ID uint `uri:"id" binding:"required,gt=0"`
}

// ---------- Обработчики ----------

// FollowHandler обрабатывает запросы подписок.
type FollowHandler struct {
	followService *FollowService
}

func NewFollowHandler(followService *FollowService) *FollowHandler {
	return &FollowHandler{followService: followService}
}

// Follow подписывает текущего пользователя на автора.
func (h *FollowHandler) Follow(c *gin.Context) {
	var req AuthorIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		appErr := apperrors.NewBadRequestError("некорректный ID автора")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}
	userID := c.GetUint("userID")
	if userID == 0 {
		appErr := apperrors.NewUnauthorizedError("требуется аутентификация")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	if err := h.followService.Follow(c.Request.Context(), userID, req.ID); err != nil {
		log.Error().Err(err).Uint("user_id", userID).Uint("author_id", req.ID).Msg("Follow: failed to follow author")
		appErr := apperrors.NewBadRequestError(err.Error())
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "followed"})
}

// Unfollow отменяет подписку.
func (h *FollowHandler) Unfollow(c *gin.Context) {
	var req AuthorIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		appErr := apperrors.NewBadRequestError("некорректный ID автора")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}
	userID := c.GetUint("userID")
	if userID == 0 {
		appErr := apperrors.NewUnauthorizedError("требуется аутентификация")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	if err := h.followService.Unfollow(c.Request.Context(), userID, req.ID); err != nil {
		log.Error().Err(err).Uint("user_id", userID).Uint("author_id", req.ID).Msg("Unfollow: failed to unfollow author")
		appErr := apperrors.NewBadRequestError(err.Error())
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "unfollowed"})
}

// IsFollowing проверяет статус подписки.
func (h *FollowHandler) IsFollowing(c *gin.Context) {
	var req AuthorIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		appErr := apperrors.NewBadRequestError("некорректный ID автора")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}
	userID := c.GetUint("userID")
	if userID == 0 {
		appErr := apperrors.NewUnauthorizedError("требуется аутентификация")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	following := h.followService.IsFollowing(c.Request.Context(), userID, req.ID)
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
		c.HTML(http.StatusInternalServerError, "errors-500.html", nil)
		return
	}

	isAdmin := middleware.IsAdmin(c)

	render.Page(c, http.StatusOK, "follow-list.html", gin.H{
		"Authors":       authors,
		"csrf":          csrf.GetToken(c),
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
	})
}
