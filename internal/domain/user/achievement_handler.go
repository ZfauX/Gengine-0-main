// internal/domain/user/achievement_handler.go
package user

import (
	"net/http"

	"gengine-0/internal/pkg/render"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

type AchievementHandler struct {
	achievementRepo AchievementRepository
}

func NewAchievementHandler(achievementRepo AchievementRepository) *AchievementHandler {
	return &AchievementHandler{achievementRepo: achievementRepo}
}

// List отображает список достижений пользователя.
// @Summary Список достижений
// @Description Отображает все достижения текущего пользователя
// @Tags achievements
// @Produce html
// @Success 200 {string} html "Страница достижений"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /achievements [get]
// @Security JWT
func (h *AchievementHandler) List(c *gin.Context) {
	userID := c.GetUint("userID")
	achievements, err := h.achievementRepo.GetByUserID(c.Request.Context(), userID)
	if err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("AchievementHandler.List: failed to fetch achievements")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	render.Page(c, http.StatusOK, "achievements-list.html", gin.H{
		"Title":         "Достижения",
		"Achievements":  achievements,
		"CurrentUserID": userID,
	})
}
