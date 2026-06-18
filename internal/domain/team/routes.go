// internal/domain/team/routes.go
package team

import (
	"gengine-0/internal/config"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func RegisterRoutes(
	router *gin.Engine,
	db *gorm.DB,
	cfg *config.Config,
	store storage.FileStorage,
	authorizer middleware.GameAuthorizer, // интерфейс вместо *game.CoAuthorService
) {
	teamService := NewTeamService(db)
	invitationService := NewInvitationService(db, teamService, authorizer, cfg)

	teamHandler := NewTeamHandler(teamService, store)
	invitationHandler := NewInvitationHandler(invitationService)

	authService := user.NewAuthService(db, cfg)
	authRequired := middleware.AuthRequired(authService)

	teamAccessChecker := &teamAccessChecker{teamService: teamService}
	teamAccess := middleware.TeamCaptainOrGameAuthor(teamAccessChecker)

	protected := router.Group("/")
	protected.Use(authRequired)

	protected.GET("/teams", teamHandler.MyTeams)
	protected.GET("/teams/:team_id", teamHandler.ViewTeam) // просмотр команды вне контекста игры
	protected.GET("/teams/new", teamHandler.NewTeamForm)
	protected.POST("/teams", teamHandler.CreateTeam)

	// Приглашения вне командного контекста (личные)
	protected.GET("/invitations/my", invitationHandler.MyInvitations)
	protected.POST("/invitations/:id/accept", invitationHandler.Accept)
	protected.POST("/invitations/:id/decline", invitationHandler.Decline)

	teamGroup := protected.Group("/games/:id/teams/:team_id")
	teamGroup.Use(teamAccess)
	{
		teamGroup.GET("/members", teamHandler.Members)
		teamGroup.GET("/members/add", teamHandler.AddMemberForm)
		teamGroup.POST("/members/add", teamHandler.AddMember)
		teamGroup.POST("/members/:member_id/remove", teamHandler.RemoveMember)
		teamGroup.GET("/change-captain", teamHandler.ChangeCaptainForm)
		teamGroup.POST("/change-captain", teamHandler.ChangeCaptain)

		teamGroup.GET("/invitations", invitationHandler.Index)
		teamGroup.GET("/invitations/new", invitationHandler.NewForm)
		teamGroup.POST("/invitations", invitationHandler.Create)
	}
}

type teamAccessChecker struct {
	teamService *TeamService
}

func (t *teamAccessChecker) CanManageTeam(teamID, userID uint) bool {
	return t.teamService.CanManageTeam(teamID, userID)
}