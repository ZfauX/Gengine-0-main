// internal/domain/team/routes.go
package team

import (
	"gengine-0/internal/config"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes СЂРµРіРёСЃС‚СЂРёСЂСѓРµС‚ РјР°СЂС€СЂСѓС‚С‹ РєРѕРјР°РЅРґ Рё РїСЂРёРіР»Р°С€РµРЅРёР№.
func RegisterRoutes(
	r *gin.RouterGroup,
	teamService *TeamService,
	invitationService *InvitationService,
	cfg *config.Config,
	localStorage storage.FileStorage,
	authorizer middleware.GameAuthorizer,
	authService *user.AuthService,
) {
	teamHandler := NewTeamHandler(teamService, localStorage)
	invitationHandler := NewInvitationHandler(invitationService)

	teamsGroup := r.Group("/teams")
	teamsGroup.Use(middleware.AuthRequired(authService))
	{
		teamsGroup.GET("/", teamHandler.MyTeams)

		teamsGroup.GET("/new", teamHandler.NewTeamForm)

		teamsGroup.POST("/new", teamHandler.CreateTeam)

		teamsGroup.GET("/:team_id", teamHandler.ViewTeam)
	}

	invitationsGroup := r.Group("/invitations")
	invitationsGroup.Use(middleware.AuthRequired(authService))
	{
		invitationsGroup.GET("/my", invitationHandler.MyInvitations)

		invitationsGroup.POST("/:id/accept", invitationHandler.Accept)

		invitationsGroup.POST("/:id/decline", invitationHandler.Decline)
	}
}
