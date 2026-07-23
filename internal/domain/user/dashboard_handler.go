// internal/domain/user/dashboard_handler.go
package user

import (
	"net/http"
	"strings"

	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

type DashboardHandler struct {
	dashboardService *UserDashboardService
	db               *gorm.DB
}

func NewDashboardHandler(dashboardService *UserDashboardService, db *gorm.DB) *DashboardHandler {
	return &DashboardHandler{dashboardService: dashboardService, db: db}
}

// Index отображает личный кабинет пользователя.
// @Summary Личный кабинет
// @Description Отображает главную страницу личного кабинета пользователя
// @Tags dashboard
// @Produce html
// @Success 200 {string} html "Личный кабинет"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /dashboard [get]
// @Security JWT
func (h *DashboardHandler) Index(c *gin.Context) {
	userID := c.GetUint("userID")
	dash, err := h.dashboardService.GetDashboard(c.Request.Context(), userID)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("DashboardHandler.Index: failed to get dashboard")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	searchQuery := strings.TrimSpace(c.Query("search"))
	if searchQuery != "" {
		lowerQuery := strings.ToLower(searchQuery)
		var filteredGames []DashboardGame
		for _, g := range dash.AuthoredGames {
			if strings.Contains(strings.ToLower(g.Name), lowerQuery) {
				filteredGames = append(filteredGames, g)
			}
		}
		dash.AuthoredGames = filteredGames

		var filteredCaptainTeams []DashboardTeamWithGame
		for _, t := range dash.CaptainTeams {
			if strings.Contains(strings.ToLower(t.Team.Name), lowerQuery) ||
				strings.Contains(strings.ToLower(t.Game.Name), lowerQuery) {
				filteredCaptainTeams = append(filteredCaptainTeams, t)
			}
		}
		dash.CaptainTeams = filteredCaptainTeams

		var filteredMemberTeams []DashboardTeamWithGame
		for _, t := range dash.MemberTeams {
			if strings.Contains(strings.ToLower(t.Team.Name), lowerQuery) ||
				strings.Contains(strings.ToLower(t.Game.Name), lowerQuery) {
				filteredMemberTeams = append(filteredMemberTeams, t)
			}
		}
		dash.MemberTeams = filteredMemberTeams

		var filteredPassings []DashboardPassingWithGame
		for _, p := range dash.ActivePassings {
			if strings.Contains(strings.ToLower(p.GameName), lowerQuery) ||
				strings.Contains(strings.ToLower(p.TeamName), lowerQuery) {
				filteredPassings = append(filteredPassings, p)
			}
		}
		dash.ActivePassings = filteredPassings
	}

	isAdmin := middleware.IsAdmin(c)
	render.Page(c, http.StatusOK, "dashboard-index.html", gin.H{
		"Dashboard":     dash,
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
		"SearchQuery":   searchQuery,
	})
}
