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
// @tags games
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
		// @Summary Список игр
		// @Description Возвращает список игр с фильтрацией и пагинацией
		// @Tags games
		// @Produce html
		// @Param status query string false "Статус игры (draft, published)"
		// @Param search query string false "Поиск по названию"
		// @Param sort query string false "Поле сортировки (created_at, name, starts_at, rating, participants)"
		// @Param order query string false "Порядок сортировки (asc, desc)"
		// @Param page query int false "Номер страницы" default(1)
		// @Param per_page query int false "Количество на странице" default(10)
		// @Param author_id query int false "ID автора"
		// @Success 200 {string} html "Страница со списком игр"
		// @Router /games [get]
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

		// @Summary Детали игры
		// @Description Показывает полную информацию об игре
		// @Tags games
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 200 {string} html "Страница игры"
		// @Failure 404 {object} map[string]interface{} "Игра не найдена"
		// @Router /games/{id} [get]
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
		// @Summary Форма создания игры
		// @Description Возвращает HTML-страницу с формой для создания новой игры
		// @Tags games
		// @Produce html
		// @Success 200 {string} html "Форма создания игры"
		// @Router /games/create [get]
		// @Security JWT
		protected.GET("/create", func(c *gin.Context) {
			c.HTML(http.StatusOK, "game_create.html", gin.H{
				"title": "Создать игру",
				"csrf":  c.GetString("csrf"),
			})
		})

		// @Summary Создание игры
		// @Description Создаёт новую игру как черновик
		// @Tags games
		// @Accept multipart/form-data
		// @Produce html
		// @Param name formData string true "Название игры"
		// @Param description formData string false "Описание игры"
		// @Param cover formData file false "Обложка игры (jpeg, png, webp)"
		// @Success 302 {string} string "Перенаправление на /games"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Router /games [post]
		// @Security JWT
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

		// @Summary Форма редактирования игры
		// @Description Возвращает HTML-страницу с формой для редактирования игры
		// @Tags games
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 200 {string} html "Форма редактирования игры"
		// @Failure 404 {object} map[string]interface{} "Игра не найдена"
		// @Router /games/{id}/edit [get]
		// @Security JWT
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

		// @Summary Обновление игры
		// @Description Обновляет данные игры
		// @Tags games
		// @Accept multipart/form-data
		// @Produce html
		// @Param id path int true "ID игры"
		// @Param name formData string false "Название игры"
		// @Param description formData string false "Описание игры"
		// @Param cover formData file false "Обложка игры"
		// @Param delete_cover formData string false "Удалить обложку (1)"
		// @Success 302 {string} string "Перенаправление на /games/{id}"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Router /games/{id}/edit [post]
		// @Security JWT
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

		// @Summary Публикация игры
		// @Description Публикует черновик игры (доступно автору или контент-менеджеру)
		// @Tags games
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 302 {string} string "Перенаправление на /games/{id}"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /games/{id}/publish [post]
		// @Security JWT
		protected.POST("/:id/publish", func(c *gin.Context) {
			id, _ := strconv.Atoi(c.Param("id"))
			if err := gameService.Publish(c.Request.Context(), uint(id), c.GetUint("user_id")); err != nil {
				c.String(http.StatusBadRequest, err.Error())
				return
			}
			c.Redirect(http.StatusFound, "/games/"+strconv.Itoa(id))
		})

		// @Summary Удаление игры
		// @Description Удаляет игру (доступно только владельцу)
		// @Tags games
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 302 {string} string "Перенаправление на /games"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /games/{id}/delete [post]
		// @Security JWT
		protected.POST("/:id/delete", func(c *gin.Context) {
			id, _ := strconv.Atoi(c.Param("id"))
			if err := gameService.Delete(c.Request.Context(), uint(id), c.GetUint("user_id")); err != nil {
				c.String(http.StatusBadRequest, err.Error())
				return
			}
			c.Redirect(http.StatusFound, "/games")
		})

		// Управление соавторами — используем методы List, Add (3 аргумента), Remove
		// @Summary Управление соавторами
		// @Description Отображает страницу управления соавторами игры
		// @Tags coauthors
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 200 {string} html "Страница управления соавторами"
		// @Router /games/{id}/coauthors [get]
		// @Security JWT
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

		// @Summary Добавление соавтора
		// @Description Добавляет нового соавтора к игре (доступно владельцу)
		// @Tags coauthors
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID игры"
		// @Param user_id formData uint true "ID пользователя"
		// @Success 302 {string} string "Перенаправление на /games/{id}/coauthors"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /games/{id}/coauthors [post]
		// @Security JWT
		protected.POST("/:id/coauthors", func(c *gin.Context) {
			id, _ := strconv.Atoi(c.Param("id"))
			userID, _ := strconv.Atoi(c.PostForm("user_id"))
			if err := coAuthorSvc.Add(uint(id), uint(userID), c.GetUint("user_id")); err != nil {
				c.String(http.StatusBadRequest, err.Error())
				return
			}
			c.Redirect(http.StatusFound, "/games/"+strconv.Itoa(id)+"/coauthors")
		})

		// @Summary Удаление соавтора
		// @Description Удаляет соавтора из игры (доступно владельцу)
		// @Tags coauthors
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID игры"
		// @Param coauthor_id path int true "ID соавтора"
		// @Success 302 {string} string "Перенаправление на /games/{id}/coauthors"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /games/{id}/coauthors/{coauthor_id} [delete]
		// @Security JWT
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

	// @Summary Snapshot игры (мониторинг)
	// @Description Возвращает JSON-снимок текущего состояния игры
	// @Tags monitor
	// @Produce json
	// @Param game_id path int true "ID игры"
	// @Success 200 {object} map[string]interface{} "Снимок игры"
	// @Failure 500 {object} map[string]interface{} "Внутренняя ошибка"
	// @Router /games/{game_id}/monitor [get]
	// @Security JWT
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
// @tags gameplay
func RegisterGameplayRoutes(
	r *gin.RouterGroup,
	handler *GameplayHandler,
	coAuthorSvc *CoAuthorService,
) {
	// @Summary Страница прохождения уровня
	// @Description Отображает страницу прохождения текущего уровня для команды
	// @Tags gameplay
	// @Produce html
	// @Param passing_id path int true "ID прохождения"
	// @Success 200 {string} html "Страница прохождения уровня"
	// @Router /game/{passing_id} [get]
	// @Security JWT
	r.GET("/game/:passing_id", handler.ShowGame)

	// @Summary Отправка кода
	// @Description Отправляет текстовый код для проверки на текущем уровне
	// @Tags gameplay
	// @Accept x-www-form-urlencoded
	// @Produce html
	// @Param passing_id path int true "ID прохождения"
	// @Param code formData string true "Код"
	// @Success 302 {string} string "Перенаправление на /game/{passing_id}"
	// @Router /game/{passing_id}/submit [post]
	// @Security JWT
	r.POST("/game/:passing_id/submit", handler.SubmitCode)

	// @Summary Использование подсказки
	// @Description Использует подсказку для текущего уровня
	// @Tags gameplay
	// @Accept x-www-form-urlencoded
	// @Produce html
	// @Param passing_id path int true "ID прохождения"
	// @Success 302 {string} string "Перенаправление на /game/{passing_id}"
	// @Router /game/{passing_id}/hint [post]
	// @Security JWT
	r.POST("/game/:passing_id/hint", handler.UseHint)

	// @Summary Отправка файлового ответа
	// @Description Отправляет файл в качестве ответа на текущем уровне
	// @Tags gameplay
	// @Accept multipart/form-data
	// @Produce html
	// @Param passing_id path int true "ID прохождения"
	// @Param answer_file formData file true "Файл (jpeg, png, gif, pdf, txt)"
	// @Success 302 {string} string "Перенаправление на /game/{passing_id}"
	// @Router /game/{passing_id}/file [post]
	// @Security JWT
	r.POST("/game/:passing_id/file", handler.SubmitFile)

	// @Summary Принятие ответа автором
	// @Description Автор принимает ответ команды (для Blackbox-уровня)
	// @Tags gameplay
	// @Accept x-www-form-urlencoded
	// @Produce html
	// @Param passing_id path int true "ID прохождения"
	// @Param game_id query int true "ID игры"
	// @Success 302 {string} string "Перенаправление на /games/{game_id}/monitor"
	// @Router /game/{passing_id}/accept [post]
	// @Security JWT
	r.POST("/game/:passing_id/accept", handler.AcceptAnswer)

	// Тестовые маршруты
	// @Summary Страница тестового прохождения
	// @Description Отображает страницу тестового прохождения уровня
	// @Tags testing
	// @Produce html
	// @Param passing_id path int true "ID прохождения"
	// @Success 200 {string} html "Страница тестового прохождения"
	// @Router /testing/{passing_id} [get]
	// @Security JWT
	r.GET("/testing/:passing_id", handler.ShowTestGame)

	// @Summary Отправка кода (тестовый режим)
	// @Description Отправляет код в тестовом режиме (всегда считается правильным)
	// @Tags testing
	// @Accept x-www-form-urlencoded
	// @Produce html
	// @Param passing_id path int true "ID прохождения"
	// @Param code formData string true "Код"
	// @Success 302 {string} string "Перенаправление на /testing/{passing_id}"
	// @Router /testing/{passing_id}/submit [post]
	// @Security JWT
	r.POST("/testing/:passing_id/submit", handler.SubmitTestCode)

	// @Summary Пропуск уровня (тестовый режим)
	// @Description Пропускает текущий уровень в тестовом прохождении
	// @Tags testing
	// @Accept x-www-form-urlencoded
	// @Produce html
	// @Param passing_id path int true "ID прохождения"
	// @Success 302 {string} string "Перенаправление на /testing/{passing_id}"
	// @Router /testing/{passing_id}/skip [post]
	// @Security JWT
	r.POST("/testing/:passing_id/skip", handler.SkipTestLevel)
}
