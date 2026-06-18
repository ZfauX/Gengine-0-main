// internal/domain/tournament/routes.go
package tournament

import (
	"net/http"
	"strconv"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func RegisterRoutes(router *gin.Engine, db *gorm.DB, cfg *config.Config) {
	teamService := team.NewTeamService(db)
	coAuthorService := game.NewCoAuthorService(db)

	gameService := game.NewGameService(db, coAuthorService, nil, nil, nil, nil, nil, cfg)
	tournamentService := NewTournamentService(db, teamService, cfg)
	tournamentHandler := NewTournamentHandler(tournamentService, teamService, gameService)

	authService := user.NewAuthService(db, cfg)
	authRequired := middleware.AuthRequired(authService)

	tournamentAuthor := func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		var t Tournament
		if err := db.First(&t, id).Error; err != nil {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		userID := c.GetUint("userID")
		if t.AuthorID != userID {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}

	router.GET("/tournaments", tournamentHandler.ListTournaments)
	router.GET("/tournaments/:id", tournamentHandler.ShowTournament)

	protected := router.Group("/")
	protected.Use(authRequired)
	{
		protected.GET("/tournaments/new", tournamentHandler.NewTournamentForm)
		protected.POST("/tournaments", tournamentHandler.CreateTournament)

		authorGroup := protected.Group("/tournaments/:id")
		authorGroup.Use(tournamentAuthor)
		{
			authorGroup.GET("/edit", tournamentHandler.EditTournamentForm)
			authorGroup.PUT("/update", tournamentHandler.UpdateTournament)
			authorGroup.GET("/games/add", tournamentHandler.AddGameForm)
			authorGroup.POST("/games/add", tournamentHandler.AddGame)
			authorGroup.POST("/games/:id/remove", tournamentHandler.RemoveGame)
		}

		protected.POST("/tournaments/:id/apply", tournamentHandler.Apply)
	}
}