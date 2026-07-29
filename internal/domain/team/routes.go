// internal/domain/team/routes.go
package team

import (
	"gengine-0/internal/config"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterRoutes регистрирует маршруты команд и приглашений.
func RegisterRoutes(
	r *gin.RouterGroup,
	db *gorm.DB,
	teamService *TeamService,
	invitationService *InvitationService,
	cfg *config.Config,
	localStorage storage.FileStorage,
	authorizer middleware.GameAuthorizer,
	authService *user.AuthService,
	hub *ws.RoomHub,
) {
	teamHandler := NewTeamHandler(teamService, localStorage)
	invitationHandler := NewInvitationHandler(invitationService)
	chatHandler := NewChatHandler(db, hub, teamService)

	teamsGroup := r.Group("/teams")
	teamsGroup.Use(middleware.AuthRequired(authService))
	{
		teamsGroup.GET("/", teamHandler.MyTeams)

		teamsGroup.GET("/new", teamHandler.NewTeamForm)

		teamsGroup.POST("/new", teamHandler.CreateTeam)

		teamsGroup.GET("/:team_id", teamHandler.ViewTeam)

		teamsGroup.GET("/:team_id/chat", chatHandler.TeamChatPage)

		teamsGroup.POST("/:team_id/members/:member_id/remove", teamHandler.RemoveMember)

		teamsGroup.GET("/:team_id/change-captain", teamHandler.ChangeCaptainForm)
		teamsGroup.POST("/:team_id/change-captain", teamHandler.ChangeCaptain)
	}

	invitationsGroup := r.Group("/invitations")
	invitationsGroup.Use(middleware.AuthRequired(authService))
	{
		invitationsGroup.GET("/my", invitationHandler.MyInvitations)

		invitationsGroup.POST("/:id/accept", invitationHandler.Accept)

		invitationsGroup.POST("/:id/decline", invitationHandler.Decline)
	}
}
