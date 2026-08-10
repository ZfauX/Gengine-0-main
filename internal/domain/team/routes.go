// internal/domain/team/routes.go
package team

import (
	"gengine-0/internal/config"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes регистрирует маршруты команд и приглашений.
func RegisterRoutes(
	r *gin.RouterGroup,
	teamService *TeamService,
	invitationService *InvitationService,
	cfg *config.Config,
	localStorage storage.FileStorage,
	authorizer middleware.GameAuthorizer,
	authService *user.AuthService,
	hub *ws.RoomHub,
) {
	teamHandler := NewTeamHandler(teamService, localStorage)
	invitationHandler := NewInvitationHandler(invitationService, teamService)
	chatHandler := NewChatHandler(hub, teamService)
	userSearchHandler := NewUserSearchHandler(teamService)

	teamsGroup := r.Group("/teams")
	teamsGroup.Use(middleware.AuthRequired(authService))
	{
		teamsGroup.GET("/", teamHandler.MyTeams)

		teamsGroup.GET("/new", teamHandler.NewTeamForm)

		teamsGroup.POST("/new", teamHandler.CreateTeam)

		teamsGroup.GET("/:team_id", teamHandler.ViewTeam)

		teamsGroup.GET("/:team_id/invitations", invitationHandler.Index)
		teamsGroup.GET("/:team_id/invitations/new", userSearchHandler.NewInvitationForm)
		teamsGroup.POST("/:team_id/invitations/new", invitationHandler.Create)

		teamsGroup.GET("/:team_id/chat", chatHandler.TeamChatPage)

		teamsGroup.POST("/:team_id/members/:member_id/remove", teamHandler.RemoveMember)

		// A-5 (pass 45): добровольный выход из команды.
		teamsGroup.POST("/:team_id/leave", teamHandler.LeaveMember)

		// A-2/A-3 (pass 45): роли и группы участников.
		teamsGroup.POST("/:team_id/members/:member_id/role", teamHandler.SetMemberRole)
		teamsGroup.POST("/:team_id/members/:member_id/group", teamHandler.SetMemberGroup)
		teamsGroup.POST("/:team_id/members/:member_id/field-role", teamHandler.SetFieldRole)

		teamsGroup.GET("/:team_id/change-captain", teamHandler.ChangeCaptainForm)
		teamsGroup.POST("/:team_id/change-captain", teamHandler.ChangeCaptain)
	}

	// API для поиска пользователей
	apiGroup := r.Group("/api/teams")
	apiGroup.Use(middleware.AuthRequired(authService))
	{
		apiGroup.GET("/:team_id/users/search", userSearchHandler.SearchUsers)
	}

	invitationsGroup := r.Group("/invitations")
	invitationsGroup.Use(middleware.AuthRequired(authService))
	{
		invitationsGroup.GET("/my", invitationHandler.MyInvitations)

		invitationsGroup.POST("/:id/accept", invitationHandler.Accept)

		invitationsGroup.POST("/:id/decline", invitationHandler.Decline)
	}
}
