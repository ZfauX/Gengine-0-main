// internal/domain/social/handler.go
package social

import (
	"errors"
	"net/http"

	apperrors "gengine-0/internal/pkg/errors"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"

	csrf "gengine-0/internal/pkg/csrf"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
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

// Follow подписывается на автора.
// @Summary Подписаться на автора
// @Tags social
// @Accept json
// @Produce json
// @Param id path int true "ID автора"
// @Success 200 {object} map[string]interface{} "Успешная подписка"
// @Failure 400 {object} map[string]interface{} "Неверный запрос"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /follow/{id} [post]
// @Security JWT
func (h *FollowHandler) Follow(c *gin.Context) {
	var req AuthorIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		appErr := apperrors.BadRequest("некорректный ID автора")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}
	userID := c.GetUint("userID")
	if userID == 0 {
		appErr := apperrors.Unauthorized("требуется аутентификация")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	if err := h.followService.Follow(c.Request.Context(), userID, req.ID); err != nil {
		switch err.Error() {
		case "нельзя подписаться на самого себя", "не подписан":
			appErr := apperrors.BadRequest(err.Error())
			c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{"error": appErr.Message, "code": appErr.Code})
		default:
			log.Error().Err(err).Uint("user_id", userID).Uint("author_id", req.ID).Msg("Follow: failed to follow author")
			appErr := apperrors.Wrap(err, "Follow: failed to follow author")
			c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{"error": appErr.Message, "code": appErr.Code})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "followed"})
}

// Unfollow отписывается от автора.
// @Summary Отписаться от автора
// @Tags social
// @Accept json
// @Produce json
// @Param id path int true "ID автора"
// @Success 200 {object} map[string]interface{} "Успешная отписка"
// @Failure 400 {object} map[string]interface{} "Неверный запрос"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /follow/{id} [delete]
// @Security JWT
func (h *FollowHandler) Unfollow(c *gin.Context) {
	var req AuthorIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		appErr := apperrors.BadRequest("некорректный ID автора")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}
	userID := c.GetUint("userID")
	if userID == 0 {
		appErr := apperrors.Unauthorized("требуется аутентификация")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	if err := h.followService.Unfollow(c.Request.Context(), userID, req.ID); err != nil {
		if errors.Is(err, ErrNotFollowing) {
			appErr := apperrors.BadRequest(err.Error())
			c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{"error": appErr.Message, "code": appErr.Code})
		} else {
			log.Error().Err(err).Uint("user_id", userID).Uint("author_id", req.ID).Msg("Unfollow: failed to unfollow author")
			appErr := apperrors.Wrap(err, "Unfollow: failed to unfollow author")
			c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{"error": appErr.Message, "code": appErr.Code})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "unfollowed"})
}

// IsFollowing проверяет, подписан ли текущий пользователь на автора.
// @Summary Проверить подписку
// @Tags social
// @Produce json
// @Param id path int true "ID автора"
// @Success 200 {object} map[string]interface{} "Результат проверки"
// @Failure 400 {object} map[string]interface{} "Неверный запрос"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /follow/{id}/check [get]
// @Security JWT
func (h *FollowHandler) IsFollowing(c *gin.Context) {
	var req AuthorIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		appErr := apperrors.BadRequest("некорректный ID автора")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}
	userID := c.GetUint("userID")
	if userID == 0 {
		appErr := apperrors.Unauthorized("требуется аутентификация")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	following := h.followService.IsFollowing(c.Request.Context(), userID, req.ID)
	c.JSON(http.StatusOK, gin.H{"following": following})
}

// Subscriptions возвращает список подписок текущего пользователя.
// @Summary Список подписок
// @Tags social
// @Produce html
// @Success 200 {string} html "Страница подписок"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /subscriptions [get]
// @Security JWT
func (h *FollowHandler) Subscriptions(c *gin.Context) {
	userID := c.GetUint("userID")
	if userID == 0 {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}

	authors, err := h.followService.GetSubscriptions(c.Request.Context(), userID)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("Subscriptions: failed to get subscriptions")
		render.RenderErrorPage(c, http.StatusInternalServerError)
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
