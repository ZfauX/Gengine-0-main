// internal/domain/team/user_search_handler.go
package team

import (
	"net/http"
	"strconv"
	"strings"

	"gengine-0/internal/pkg/render"

	csrf "gengine-0/internal/pkg/csrf"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// UserSearchHandler обрабатывает поиск пользователей для приглашений.
type UserSearchHandler struct {
	db      *gorm.DB
	teamSvc *TeamService
}

// NewUserSearchHandler создаёт обработчик поиска пользователей.
func NewUserSearchHandler(db *gorm.DB, teamSvc *TeamService) *UserSearchHandler {
	return &UserSearchHandler{db: db, teamSvc: teamSvc}
}

// SearchUsers ищет пользователей по имени или email (JSON).
func (h *UserSearchHandler) SearchUsers(c *gin.Context) {
	query := strings.TrimSpace(c.Query("q"))
	if query == "" || len(query) < 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "запрос должен содержать минимум 2 символа"})
		return
	}

	teamID, _ := strconv.Atoi(c.Param("team_id"))

	var users []struct {
		ID   uint   `json:"id"`
		Name string `json:"name"`
	}

	err := h.db.WithContext(c.Request.Context()).Table("users").
		Select("id, name").
		Where("LOWER(name) LIKE ? OR LOWER(email) LIKE ?",
			"%"+strings.ToLower(query)+"%",
			"%"+strings.ToLower(query)+"%").
		Limit(20).
		Scan(&users).Error
	if err != nil {
		log.Error().Err(err).Msg("UserSearch: search failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "ошибка поиска"})
		return
	}

	// Исключаем уже состоящих в команде
	var memberIDs []uint
	if teamID > 0 {
		h.db.WithContext(c.Request.Context()).Table("team_members").
			Where("team_id = ?", teamID).Pluck("user_id", &memberIDs)
		// Добавляем капитана
		var captID uint
		h.db.WithContext(c.Request.Context()).Table("teams").
			Where("id = ?", teamID).Pluck("captain_id", &captID)
		memberIDs = append(memberIDs, captID)
	}

	filtered := make([]gin.H, 0, len(users))
	for _, u := range users {
		isMember := false
		for _, mid := range memberIDs {
			if u.ID == mid {
				isMember = true
				break
			}
		}
		if !isMember {
			filtered = append(filtered, gin.H{
				"id":   u.ID,
				"name": u.Name,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{"users": filtered})
}

// NewInvitationForm показывает форму приглашения с поиском пользователей.
func (h *UserSearchHandler) NewInvitationForm(c *gin.Context) {
	teamID, err := strconv.Atoi(c.Param("team_id"))
	if err != nil || teamID <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_team_id"))
		return
	}

	userID := c.GetUint("userID")

	render.Page(c, http.StatusOK, "invitations-new.html", gin.H{
		"Title":         "Новое приглашение",
		"TeamID":        uint(teamID),
		"csrf":          csrf.GetToken(c),
		"CurrentUserID": userID,
	})
}
