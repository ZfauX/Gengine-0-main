// internal/domain/tournament/routes.go
package tournament

import (
	"gengine-0/internal/config"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes СЂРµРіРёСЃС‚СЂРёСЂСѓРµС‚ РјР°СЂС€СЂСѓС‚С‹ С‚СѓСЂРЅРёСЂРѕРІ.
func RegisterRoutes(
	r *gin.RouterGroup,
	tournamentService *TournamentService,
	teamService *team.TeamService,
	cfg *config.Config,
	authService *user.AuthService,
) {
	handler := NewTournamentHandler(tournamentService, teamService, cfg)

	public := r.Group("/tournaments")
	{
		public.GET("/", handler.List)

		public.GET("/:id", handler.Show)
	}

	protected := r.Group("/tournaments")
	protected.Use(middleware.AuthRequired(authService))
	{
		protected.GET("/new", handler.NewForm)

		protected.POST("/new", handler.Create)

		protected.GET("/:id/edit", handler.EditForm)

		protected.POST("/:id/edit", handler.Update)

		protected.GET("/:id/games", handler.Games)

		protected.POST("/:id/games/add", handler.AddGame)

		protected.POST("/:id/games/:game_id/remove", handler.RemoveGame)

		protected.GET("/:id/apply", handler.ApplyForm)

		protected.POST("/:id/apply", handler.Apply)
	}
}
