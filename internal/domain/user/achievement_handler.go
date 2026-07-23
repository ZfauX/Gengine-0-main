// internal/domain/user/achievement_handler.go
package user

import (
	"net/http"

	"gengine-0/internal/pkg/render"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

type AchievementHandler struct {
	db *gorm.DB
}

func NewAchievementHandler(db *gorm.DB) *AchievementHandler {
	return &AchievementHandler{db: db}
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
	var achievements []Achievement
	if err := h.db.Joins("JOIN user_achievements ON user_achievements.achievement_id = achievements.id").
		Where("user_achievements.user_id = ?", userID).
		Find(&achievements).Error; err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("AchievementHandler.List: failed to fetch achievements")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	render.Page(c, http.StatusOK, "achievements-list.html", gin.H{
		"Achievements":  achievements,
		"CurrentUserID": userID,
	})
}
