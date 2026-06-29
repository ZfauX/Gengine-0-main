// internal/domain/game/routes.go
package game

import (
	"gengine-0/internal/config"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/audit"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes регистрирует маршруты для игр, используя готовые обработчики.
// @tags games
// @tags coauthors
// @tags passings
// @tags gameplay
func RegisterRoutes(
	r *gin.Engine,
	gameService *GameService,
	passingService *GamePassingService, // теперь нужен для Apply
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
	gameHandler := NewGameHandler(
		gameService,
		passingService,
		coAuthorSvc,
		nil, // noteService
		nil, // simulateService
		nil, // photoService
		localStorage,
		hub,
		auditSvc,
		nil, // db не нужен для базовых операций
	)

	public := r.Group("/games")
	{
		// @Summary Список игр
		// @Description Возвращает страницу со списком игр с фильтрацией и пагинацией
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
		public.GET("/", gameHandler.List)

		// @Summary Детали игры
		// @Description Показывает полную информацию об игре
		// @Tags games
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 200 {string} html "Страница игры"
		// @Failure 404 {object} map[string]interface{} "Игра не найдена"
		// @Router /games/{id} [get]
		public.GET("/:id", gameHandler.Show)
	}

	protected := r.Group("/games")
	protected.Use(middleware.AuthRequired(authService))
	{
		// @Summary Форма создания игры
		// @Description Возвращает HTML-страницу с формой для создания новой игры
		// @Tags games
		// @Produce html
		// @Success 200 {string} html "Форма создания игры"
		// @Router /games/new [get]
		// @Security JWT
		protected.GET("/new", gameHandler.NewForm)

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
		protected.POST("/new", gameHandler.Create)

		// @Summary Форма редактирования игры
		// @Description Возвращает HTML-страницу с формой для редактирования игры
		// @Tags games
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 200 {string} html "Форма редактирования игры"
		// @Failure 404 {object} map[string]interface{} "Игра не найдена"
		// @Router /games/{id}/edit [get]
		// @Security JWT
		protected.GET("/:id/edit", gameHandler.EditForm)

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
		protected.POST("/:id/edit", gameHandler.Update)

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
		protected.POST("/:id/delete", gameHandler.Delete)

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
		protected.POST("/:id/publish", gameHandler.Publish)

		// @Summary Управление соавторами
		// @Description Отображает страницу управления соавторами игры
		// @Tags coauthors
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 200 {string} html "Страница управления соавторами"
		// @Router /games/{id}/co-authors [get]
		// @Security JWT
		protected.GET("/:id/co-authors", gameHandler.ManageCoAuthors)

		// @Summary Добавление соавтора
		// @Description Добавляет нового соавтора к игре (доступно владельцу)
		// @Tags coauthors
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID игры"
		// @Param user_id formData uint true "ID пользователя"
		// @Success 302 {string} string "Перенаправление на /games/{id}/co-authors"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /games/{id}/co-authors [post]
		// @Security JWT
		protected.POST("/:id/co-authors", gameHandler.AddCoAuthor)

		// @Summary Удаление соавтора
		// @Description Удаляет соавтора из игры (доступно владельцу)
		// @Tags coauthors
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID игры"
		// @Param user_id path int true "ID соавтора"
		// @Success 302 {string} string "Перенаправление на /games/{id}/co-authors"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /games/{id}/co-authors/{user_id}/delete [post]
		// @Security JWT
		protected.POST("/:id/co-authors/:user_id/delete", gameHandler.RemoveCoAuthor)

		// @Summary Список заявок и прохождений
		// @Description Отображает все заявки и текущие прохождения игры
		// @Tags passings
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 200 {string} html "Страница с прохождениями"
		// @Router /games/{id}/passings [get]
		// @Security JWT
		protected.GET("/:id/passings", gameHandler.ListPassings)

		// @Summary Изменение статуса заявки
		// @Description Принимает или отклоняет заявку команды на участие в игре
		// @Tags passings
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID игры"
		// @Param passing_id path int true "ID заявки"
		// @Param status formData string true "Новый статус (accepted / rejected)"
		// @Success 302 {string} string "Перенаправление на /games/{id}/passings"
		// @Failure 400 {object} map[string]interface{} "Недопустимый статус"
		// @Router /games/{id}/passings/{passing_id}/status [post]
		// @Security JWT
		protected.POST("/:id/passings/:passing_id/status", gameHandler.UpdatePassingStatus)

		// @Summary Запуск игры
		// @Description Запускает игру для конкретного прохождения
		// @Tags passings
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID игры"
		// @Param passing_id path int true "ID прохождения"
		// @Success 302 {string} string "Перенаправление на /games/{id}/monitor"
		// @Router /games/{id}/passings/{passing_id}/start [post]
		// @Security JWT
		protected.POST("/:id/passings/:passing_id/start", gameHandler.StartGame)

		// @Summary Подача заявки на игру (форма)
		// @Description Возвращает страницу выбора команды для подачи заявки
		// @Tags passings
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 200 {string} html "Форма подачи заявки"
		// @Router /games/{id}/apply [get]
		// @Security JWT
		protected.GET("/:id/apply", gameHandler.ApplyForm)

		// @Summary Подача заявки на игру
		// @Description Подаёт заявку от команды на участие в игре
		// @Tags passings
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID игры"
		// @Param team_id formData uint true "ID команды"
		// @Success 302 {string} string "Перенаправление на /games/{id}"
		// @Failure 400 {object} map[string]interface{} "Ошибка"
		// @Router /games/{id}/apply [post]
		// @Security JWT
		protected.POST("/:id/apply", gameHandler.Apply)

		// @Summary Симуляция прохождения
		// @Description Запускает симуляцию игры для проверки логики
		// @Tags games
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 200 {string} html "Результаты симуляции"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /games/{id}/simulate [get]
		// @Security JWT
		protected.GET("/:id/simulate", gameHandler.Simulate)

		// @Summary Настройки игры
		// @Description Отображает страницу с настройками игры
		// @Tags games
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 200 {string} html "Страница настроек"
		// @Failure 404 {object} map[string]interface{} "Игра не найдена"
		// @Router /games/{id}/settings [get]
		// @Security JWT
		protected.GET("/:id/settings", gameHandler.SettingsPage)

		// @Summary Сохранение настроек
		// @Description Сохраняет настройки игры
		// @Tags games
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 302 {string} string "Перенаправление на /games/{id}/settings"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Router /games/{id}/settings [post]
		// @Security JWT
		protected.POST("/:id/settings", gameHandler.SaveSettings)

		// @Summary Тестовые прохождения
		// @Description Отображает страницу управления тестовыми прохождениями игры
		// @Tags games
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 200 {string} html "Страница тестовых прохождений"
		// @Router /games/{id}/test [get]
		// @Security JWT
		protected.GET("/:id/test", gameHandler.TestPage)

		// @Summary Фотогалерея игры
		// @Description Отображает страницу с фотографиями игры
		// @Tags games
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 200 {string} html "Страница фотогалереи"
		// @Router /games/{id}/photos [get]
		protected.GET("/:id/photos", gameHandler.PhotosPage)

		// @Summary Загрузка фото
		// @Description Загружает новое фото в галерею игры
		// @Tags games
		// @Accept multipart/form-data
		// @Produce json
		// @Param id path int true "ID игры"
		// @Param photo formData file true "Файл фотографии"
		// @Success 200 {object} map[string]interface{} "Загруженное фото"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Router /games/{id}/photos [post]
		// @Security JWT
		protected.POST("/:id/photos", gameHandler.UploadPhoto)

		// @Summary Удаление фото
		// @Description Удаляет фото из галереи
		// @Tags games
		// @Produce json
		// @Param id path int true "ID игры"
		// @Param photo_id path int true "ID фото"
		// @Success 200 {object} map[string]interface{} "Статус удаления"
		// @Failure 404 {object} map[string]interface{} "Фото не найдено"
		// @Router /games/{id}/photos/{photo_id} [delete]
		// @Security JWT
		protected.DELETE("/:id/photos/:photo_id", gameHandler.DeletePhoto)
	}
}

// RegisterGameplayRoutes регистрирует маршруты игрового процесса.
// @tags gameplay
// @tags testing
func RegisterGameplayRoutes(
	r *gin.RouterGroup,
	handler *GameplayHandler,
	coAuthorSvc *CoAuthorService,
) {
	// ... (без изменений)
	r.GET("/game/:passing_id", handler.ShowGame)
	r.POST("/game/:passing_id/submit", handler.SubmitCode)
	r.POST("/game/:passing_id/hint", handler.UseHint)
	r.POST("/game/:passing_id/file", handler.SubmitFile)
	r.POST("/game/:passing_id/accept", handler.AcceptAnswer)

	r.GET("/testing/:passing_id", handler.ShowTestGame)
	r.POST("/testing/:passing_id/submit", handler.SubmitTestCode)
	r.POST("/testing/:passing_id/skip", handler.SkipTestLevel)
}
