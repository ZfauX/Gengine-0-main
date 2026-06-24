// internal/domain/game/routes.go
package game

import (
	"net/http"
	"strconv"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/audit"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes регистрирует маршруты для игр.
func RegisterRoutes(
	r *gin.Engine,
	gameService *GameService,
	coAuthorSvc *CoAuthorService,
	attemptSvc *AttemptService,
	progressSvc *LevelProgressService,
	monitorSvc *MonitorService,
	localStorage storage.FileStorage,
	hub *ws.RoomHub,
	cfg *config.Config,
	auditSvc *audit.Service,
	authService *user.AuthService,
) {
	// Публичные маршруты (список игр, просмотр)
	public := r.Group("/games")
	{
		public.GET("/", func(c *gin.Context) {
			filter := GameFilter{
				ViewerID: c.GetUint("user_id"),
				Status:   c.Query("status"),
				Search:   c.Query("search"),
				DateFrom: c.Query("date_from"),
				DateTo:   c.Query("date_to"),
			}
			if authorIDStr := c.Query("author_id"); authorIDStr != "" {
				id, _ := strconv.Atoi(authorIDStr)
				uid := uint(id)
				filter.AuthorID = &uid
			}
			page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
			perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "10"))

			var sort *GameSort
			if sortField := c.Query("sort"); sortField != "" {
				sortOrder := SortAsc
				if c.Query("order") == "desc" {
					sortOrder = SortDesc
				}
				sort = &GameSort{Field: sortField, Order: sortOrder}
			}

			games, total, err := gameService.ListFilteredPaginated(c.Request.Context(), filter, sort, page, perPage)
			if err != nil {
				c.String(http.StatusInternalServerError, err.Error())
				return
			}
			c.HTML(http.StatusOK, "games_list.html", gin.H{
				"title":   "Игры",
				"games":   games,
				"total":   total,
				"page":    page,
				"perPage": perPage,
			})
		})

		public.GET("/:id", func(c *gin.Context) {
			id, _ := strconv.Atoi(c.Param("id"))
			game, err := gameService.GetByID(c.Request.Context(), uint(id), c.GetUint("user_id"))
			if err != nil {
				c.String(http.StatusNotFound, "Игра не найдена")
				return
			}
			c.HTML(http.StatusOK, "game_detail.html", gin.H{
				"title": game.Name,
				"game":  game,
			})
		})
	}

	// Защищённые маршруты (создание, редактирование, управление)
	protected := r.Group("/games")
	protected.Use(middleware.AuthRequired(authService))
	{
		protected.GET("/create", func(c *gin.Context) {
			c.HTML(http.StatusOK, "game_create.html", gin.H{
				"title": "Создать игру",
				"csrf":  c.GetString("csrf"),
			})
		})
		protected.POST("/create", func(c *gin.Context) {
			var game Game
			if err := c.ShouldBind(&game); err != nil {
				c.HTML(http.StatusBadRequest, "game_create.html", gin.H{"error": err.Error()})
				return
			}
			if err := gameService.Create(c.Request.Context(), &game, c.GetUint("user_id")); err != nil {
				c.HTML(http.StatusBadRequest, "game_create.html", gin.H{"error": err.Error()})
				return
			}
			c.Redirect(http.StatusFound, "/games/"+strconv.Itoa(int(game.ID)))
		})

		protected.GET("/:id/edit", func(c *gin.Context) {
			id, _ := strconv.Atoi(c.Param("id"))
			game, err := gameService.GetByID(c.Request.Context(), uint(id), c.GetUint("user_id"))
			if err != nil {
				c.String(http.StatusNotFound, err.Error())
				return
			}
			c.HTML(http.StatusOK, "game_edit.html", gin.H{
				"title": "Редактировать игру",
				"game":  game,
				"csrf":  c.GetString("csrf"),
			})
		})
		protected.POST("/:id/edit", func(c *gin.Context) {
			id, _ := strconv.Atoi(c.Param("id"))
			var updated Game
			if err := c.ShouldBind(&updated); err != nil {
				c.HTML(http.StatusBadRequest, "game_edit.html", gin.H{"error": err.Error()})
				return
			}
			if err := gameService.Update(c.Request.Context(), uint(id), &updated, c.GetUint("user_id")); err != nil {
				c.HTML(http.StatusBadRequest, "game_edit.html", gin.H{"error": err.Error()})
				return
			}
			c.Redirect(http.StatusFound, "/games/"+strconv.Itoa(id))
		})

		protected.POST("/:id/publish", func(c *gin.Context) {
			id, _ := strconv.Atoi(c.Param("id"))
			if err := gameService.Publish(c.Request.Context(), uint(id), c.GetUint("user_id")); err != nil {
				c.String(http.StatusBadRequest, err.Error())
				return
			}
			c.Redirect(http.StatusFound, "/games/"+strconv.Itoa(id))
		})

		protected.POST("/:id/delete", func(c *gin.Context) {
			id, _ := strconv.Atoi(c.Param("id"))
			if err := gameService.Delete(c.Request.Context(), uint(id), c.GetUint("user_id")); err != nil {
				c.String(http.StatusBadRequest, err.Error())
				return
			}
			c.Redirect(http.StatusFound, "/games")
		})

		// Управление соавторами — используем методы List, Add (3 аргумента), Remove
		protected.GET("/:id/coauthors", func(c *gin.Context) {
			id, _ := strconv.Atoi(c.Param("id"))
			coauthors, err := coAuthorSvc.List(uint(id))
			if err != nil {
				c.String(http.StatusInternalServerError, err.Error())
				return
			}
			c.HTML(http.StatusOK, "coauthors.html", gin.H{
				"title":     "Соавторы",
				"coauthors": coauthors,
				"gameID":    id,
				"csrf":      c.GetString("csrf"),
			})
		})
		protected.POST("/:id/coauthors", func(c *gin.Context) {
			id, _ := strconv.Atoi(c.Param("id"))
			userID, _ := strconv.Atoi(c.PostForm("user_id"))
			// role не передаём, так как метод Add не принимает role
			if err := coAuthorSvc.Add(uint(id), uint(userID), c.GetUint("user_id")); err != nil {
				c.String(http.StatusBadRequest, err.Error())
				return
			}
			c.Redirect(http.StatusFound, "/games/"+strconv.Itoa(id)+"/coauthors")
		})
		protected.POST("/:id/coauthors/:coauthor_id/delete", func(c *gin.Context) {
			id, _ := strconv.Atoi(c.Param("id"))
			coauthorID, _ := strconv.Atoi(c.Param("coauthor_id"))
			if err := coAuthorSvc.Remove(uint(id), uint(coauthorID), c.GetUint("user_id")); err != nil {
				c.String(http.StatusBadRequest, err.Error())
				return
			}
			c.Redirect(http.StatusFound, "/games/"+strconv.Itoa(id)+"/coauthors")
		})
	}

	// WebSocket — временно отключён, так как Hub не имеет HandleWebSocket
	// r.GET("/ws/game/:game_id", func(c *gin.Context) {
	// 	gameID, _ := strconv.Atoi(c.Param("game_id"))
	// 	userID := c.GetUint("user_id")
	// 	if userID == 0 {
	// 		c.String(http.StatusUnauthorized, "Требуется авторизация")
	// 		return
	// 	}
	// 	// hub.HandleWebSocket(c.Writer, c.Request, uint(gameID), userID)
	// })

	// Мониторинг игры (snapshot)
	protected.GET("/monitor/:game_id", func(c *gin.Context) {
		gameID, _ := strconv.Atoi(c.Param("game_id"))
		snapshot, err := monitorSvc.GetOrFetchSnapshot(uint(gameID))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, snapshot)
	})
}

// RegisterGameplayRoutes регистрирует маршруты для игрового процесса.
func RegisterGameplayRoutes(
	r *gin.RouterGroup,
	handler *GameplayHandler,
	coAuthorSvc *CoAuthorService,
) {
	r.GET("/game/:passing_id", handler.ShowGame)
	r.POST("/game/:passing_id/submit", handler.SubmitCode)
	r.POST("/game/:passing_id/hint", handler.UseHint)
	r.POST("/game/:passing_id/file", handler.SubmitFile)
	r.POST("/game/:passing_id/accept", handler.AcceptAnswer)

	r.GET("/testing/:passing_id", handler.ShowTestGame)
	r.POST("/testing/:passing_id/submit", handler.SubmitTestCode)
	r.POST("/testing/:passing_id/skip", handler.SkipTestLevel)
}
