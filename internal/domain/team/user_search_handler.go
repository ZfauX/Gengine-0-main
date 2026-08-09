// internal/domain/team/user_search_handler.go
package team

import (
	"net/http"
	"strconv"
	"strings"

	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"

	csrf "gengine-0/internal/pkg/csrf"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// UserSearchHandler обрабатывает поиск пользователей для приглашений.
type UserSearchHandler struct {
	teamSvc *TeamService
}

// NewUserSearchHandler создаёт обработчик поиска пользователей.
func NewUserSearchHandler(teamSvc *TeamService) *UserSearchHandler {
	return &UserSearchHandler{teamSvc: teamSvc}
}

// SearchUsers ищет пользователей по имени или email (JSON).
// S-45-1 (pass 45): поиск доступен только капитану команды или админу —
// раньше любой авторизованный мог перебирать пользователей (enumeration).
func (h *UserSearchHandler) SearchUsers(c *gin.Context) {
	query := strings.TrimSpace(c.Query("q"))
	if query == "" || len(query) < 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "запрос должен содержать минимум 2 символа"})
		return
	}

	teamID, _ := strconv.Atoi(c.Param("team_id"))
	userID := c.GetUint("userID")

	if !middleware.IsAdmin(c) && !h.teamSvc.CanManageTeam(c.Request.Context(), uint(teamID), userID) {
		c.JSON(http.StatusForbidden, gin.H{"error": render.Tr(c, "handler.forbidden")})
		return
	}

	// Поиск + исключение участников/капитана выполняется на стороне репозитория (C1).
	users, err := h.teamSvc.SearchUsersForInvitation(c.Request.Context(), query, uint(teamID))
	if err != nil {
		log.Error().Err(err).Msg("UserSearch: search failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "ошибка поиска"})
		return
	}

	filtered := make([]gin.H, 0, len(users))
	for _, u := range users {
		filtered = append(filtered, gin.H{
			"id":   u.ID,
			"name": u.Name,
		})
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
