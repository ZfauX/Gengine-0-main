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
		// @Summary Список турниров
		// @Description Возвращает HTML-страницу со списком всех турниров
		// @Tags tournaments
		// @Produce html
		// @Success 200 {string} html "Страница со списком турниров"
		// @Router /tournaments [get]
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

		// @Summary Детали турнира
		// @Description Отображает информацию о турнире, список игр и таблицу лидеров
		// @Tags tournaments
		// @Produce html
		// @Param id path int true "ID турнира"
		// @Success 200 {string} html "Страница турнира"
		// @Failure 404 {object} map[string]interface{} "Турнир не найден"
		// @Router /tournaments/{id} [get]
		// @Security JWT
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
		// @Summary Форма создания турнира
		// @Description Возвращает HTML-страницу с формой для создания нового турнира
		// @Tags tournaments
		// @Produce html
		// @Success 200 {string} html "Форма создания турнира"
		// @Router /tournaments/create [get]
		// @Security JWT
		protected.GET("/create", func(c *gin.Context) {
			c.HTML(http.StatusOK, "tournament_create.html", gin.H{
				"title": "Создать турнир",
				"csrf":  c.GetString("csrf"),
			})
		})

		// @Summary Создание турнира
		// @Description Создаёт новый турнир
		// @Tags tournaments
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param name formData string true "Название турнира"
		// @Param description formData string false "Описание турнира"
		// @Param points_for_first formData int false "Очков за 1 место" default(10)
		// @Param points_for_second formData int false "Очков за 2 место" default(7)
		// @Param points_for_third formData int false "Очков за 3 место" default(5)
		// @Param points_for_participation formData int false "Очков за участие" default(2)
		// @Success 302 {string} string "Перенаправление на /tournaments/{id}"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Router /tournaments [post]
		// @Security JWT
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

		// @Summary Форма редактирования турнира
		// @Description Возвращает HTML-страницу с формой для редактирования турнира
		// @Tags tournaments
		// @Produce html
		// @Param id path int true "ID турнира"
		// @Success 200 {string} html "Форма редактирования турнира"
		// @Failure 404 {object} map[string]interface{} "Турнир не найден"
		// @Router /tournaments/{id}/edit [get]
		// @Security JWT
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

		// @Summary Обновление турнира
		// @Description Обновляет данные турнира (доступно только автору)
		// @Tags tournaments
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID турнира"
		// @Param name formData string false "Название турнира"
		// @Param description formData string false "Описание турнира"
		// @Param points_for_first formData int false "Очков за 1 место"
		// @Param points_for_second formData int false "Очков за 2 место"
		// @Param points_for_third formData int false "Очков за 3 место"
		// @Param points_for_participation formData int false "Очков за участие"
		// @Success 302 {string} string "Перенаправление на /tournaments/{id}"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /tournaments/{id} [put]
		// @Security JWT
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

		// @Summary Список игр турнира
		// @Description Отображает список игр, включённых в турнир, и доступные для добавления
		// @Tags tournaments
		// @Produce html
		// @Param id path int true "ID турнира"
		// @Success 200 {string} html "Страница управления играми турнира"
		// @Router /tournaments/{id}/games [get]
		// @Security JWT
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

		// @Summary Добавление игры в турнир
		// @Description Добавляет существующую игру в турнир (доступно автору турнира)
		// @Tags tournaments
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID турнира"
		// @Param game_id formData uint true "ID игры"
		// @Success 302 {string} string "Перенаправление на /tournaments/{id}/games"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /tournaments/{id}/games [post]
		// @Security JWT
		protected.POST("/:id/games/add", func(c *gin.Context) {
			id, _ := strconv.Atoi(c.Param("id"))
			gameID, _ := strconv.Atoi(c.PostForm("game_id"))
			if err := tournamentService.AddGame(c.Request.Context(), uint(id), uint(gameID), c.GetUint("user_id")); err != nil {
				c.String(http.StatusBadRequest, err.Error())
				return
			}
			c.Redirect(http.StatusFound, "/tournaments/"+strconv.Itoa(id)+"/games")
		})

		// @Summary Удаление игры из турнира
		// @Description Удаляет игру из турнира (доступно автору турнира)
		// @Tags tournaments
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID турнира"
		// @Param game_id path int true "ID игры"
		// @Success 302 {string} string "Перенаправление на /tournaments/{id}/games"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /tournaments/{id}/games/{game_id} [delete]
		// @Security JWT
		protected.POST("/:id/games/:game_id/remove", func(c *gin.Context) {
			id, _ := strconv.Atoi(c.Param("id"))
			gameID, _ := strconv.Atoi(c.Param("game_id"))
			if err := tournamentService.RemoveGame(c.Request.Context(), uint(id), uint(gameID), c.GetUint("user_id")); err != nil {
				c.String(http.StatusBadRequest, err.Error())
				return
			}
			c.Redirect(http.StatusFound, "/tournaments/"+strconv.Itoa(id)+"/games")
		})

		// @Summary Подача заявки на турнир
		// @Description Команда подаёт заявку на участие в турнире (доступно капитану)
		// @Tags tournaments
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID турнира"
		// @Param team_id formData uint true "ID команды"
		// @Success 302 {string} string "Перенаправление на /tournaments/{id}"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /tournaments/{id}/apply [post]
		// @Security JWT
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
