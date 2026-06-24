// internal/domain/tournament/routes.go
package tournament

import (
	"net/http"
	"strconv"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(
	r *gin.Engine,
	tournamentService *TournamentService,
	cfg *config.Config,
	authService *user.AuthService,
) {
	public := r.Group("/tournaments")
	{
		public.GET("/", func(c *gin.Context) {
			tournaments, err := tournamentService.List(c.Request.Context())
			if err != nil {
				c.String(http.StatusInternalServerError, err.Error())
				return
			}
			c.HTML(http.StatusOK, "tournaments_list.html", gin.H{
				"title":       "Турниры",
				"tournaments": tournaments,
			})
		})

		public.GET("/:id", func(c *gin.Context) {
			id, _ := strconv.Atoi(c.Param("id"))
			t, err := tournamentService.GetByID(c.Request.Context(), uint(id))
			if err != nil {
				c.String(http.StatusNotFound, err.Error())
				return
			}
			leaderboard, _ := tournamentService.GetLeaderboard(c.Request.Context(), uint(id))
			c.HTML(http.StatusOK, "tournament_detail.html", gin.H{
				"title":       t.Name,
				"tournament":  t,
				"leaderboard": leaderboard,
			})
		})
	}

	protected := r.Group("/tournaments")
	protected.Use(middleware.AuthRequired(authService))
	{
		protected.GET("/create", func(c *gin.Context) {
			c.HTML(http.StatusOK, "tournament_create.html", gin.H{
				"title": "Создать турнир",
				"csrf":  c.GetString("csrf"),
			})
		})
		protected.POST("/create", func(c *gin.Context) {
			var t Tournament
			if err := c.ShouldBind(&t); err != nil {
				c.HTML(http.StatusBadRequest, "tournament_create.html", gin.H{"error": err.Error()})
				return
			}
			t.AuthorID = c.GetUint("user_id")
			if err := tournamentService.Create(c.Request.Context(), &t); err != nil {
				c.HTML(http.StatusBadRequest, "tournament_create.html", gin.H{"error": err.Error()})
				return
			}
			c.Redirect(http.StatusFound, "/tournaments/"+strconv.Itoa(int(t.ID)))
		})

		protected.GET("/:id/edit", func(c *gin.Context) {
			id, _ := strconv.Atoi(c.Param("id"))
			t, err := tournamentService.GetByID(c.Request.Context(), uint(id))
			if err != nil {
				c.String(http.StatusNotFound, err.Error())
				return
			}
			if t.AuthorID != c.GetUint("user_id") {
				c.String(http.StatusForbidden, "Только автор может редактировать")
				return
			}
			c.HTML(http.StatusOK, "tournament_edit.html", gin.H{
				"title":      "Редактировать турнир",
				"tournament": t,
				"csrf":       c.GetString("csrf"),
			})
		})
		protected.POST("/:id/edit", func(c *gin.Context) {
			id, _ := strconv.Atoi(c.Param("id"))
			var updated Tournament
			if err := c.ShouldBind(&updated); err != nil {
				c.HTML(http.StatusBadRequest, "tournament_edit.html", gin.H{"error": err.Error()})
				return
			}
			if err := tournamentService.Update(c.Request.Context(), uint(id), &updated, c.GetUint("user_id")); err != nil {
				c.HTML(http.StatusBadRequest, "tournament_edit.html", gin.H{"error": err.Error()})
				return
			}
			c.Redirect(http.StatusFound, "/tournaments/"+strconv.Itoa(id))
		})

		protected.GET("/:id/games", func(c *gin.Context) {
			id, _ := strconv.Atoi(c.Param("id"))
			games, err := tournamentService.ListGames(c.Request.Context(), uint(id))
			if err != nil {
				c.String(http.StatusInternalServerError, err.Error())
				return
			}
			available, _ := tournamentService.GetAvailableGames(c.Request.Context(), uint(id), c.GetUint("user_id"))
			c.HTML(http.StatusOK, "tournament_games.html", gin.H{
				"title":        "Игры турнира",
				"games":        games,
				"available":    available,
				"tournamentID": id,
				"csrf":         c.GetString("csrf"),
			})
		})
		protected.POST("/:id/games/add", func(c *gin.Context) {
			id, _ := strconv.Atoi(c.Param("id"))
			gameID, _ := strconv.Atoi(c.PostForm("game_id"))
			if err := tournamentService.AddGame(c.Request.Context(), uint(id), uint(gameID), c.GetUint("user_id")); err != nil {
				c.String(http.StatusBadRequest, err.Error())
				return
			}
			c.Redirect(http.StatusFound, "/tournaments/"+strconv.Itoa(id)+"/games")
		})
		protected.POST("/:id/games/:game_id/remove", func(c *gin.Context) {
			id, _ := strconv.Atoi(c.Param("id"))
			gameID, _ := strconv.Atoi(c.Param("game_id"))
			if err := tournamentService.RemoveGame(c.Request.Context(), uint(id), uint(gameID), c.GetUint("user_id")); err != nil {
				c.String(http.StatusBadRequest, err.Error())
				return
			}
			c.Redirect(http.StatusFound, "/tournaments/"+strconv.Itoa(id)+"/games")
		})

		protected.POST("/:id/apply", func(c *gin.Context) {
			id, _ := strconv.Atoi(c.Param("id"))
			teamID, _ := strconv.Atoi(c.PostForm("team_id"))
			if err := tournamentService.Apply(c.Request.Context(), uint(id), uint(teamID), c.GetUint("user_id")); err != nil {
				c.String(http.StatusBadRequest, err.Error())
				return
			}
			c.Redirect(http.StatusFound, "/tournaments/"+strconv.Itoa(id))
		})
	}
}
