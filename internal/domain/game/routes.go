// internal/domain/game/routes.go
package game

import (
	"time"

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
	passingService *GamePassingService,
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
		// @Description Возвращает страницу со списком игр с фильтрацией и пагинацией.
		// @Description Доступны фильтры: по статусу (draft/published), поиск по названию, диапазон дат, автор.
		// @Description Сортировка по created_at, name, starts_at, rating (средний рейтинг), participants (количество участвующих команд).
		// @Tags games
		// @Produce html
		// @Param status query string false "Статус игры (draft, published)"
		// @Param search query string false "Поиск по названию"
		// @Param sort query string false "Поле сортировки (created_at, name, starts_at, rating, participants)" default(created_at)
		// @Param order query string false "Порядок сортировки (asc, desc)" default(desc)
		// @Param page query int false "Номер страницы" default(1)
		// @Param per_page query int false "Количество на странице" default(20) maximum(100)
		// @Param author_id query int false "ID автора"
		// @Param date_from query string false "Дата начала (с) в формате YYYY-MM-DD"
		// @Param date_to query string false "Дата начала (по) в формате YYYY-MM-DD"
		// @Success 200 {string} html "Страница со списком игр"
		// @Router /games [get]
		public.GET("/", gameHandler.List)

		// @Summary Детали игры
		// @Description Показывает полную информацию об игре: название, описание, автор, даты, рейтинг, отзывы, уровни.
		// @Description Если игра является черновиком или приватной, доступна только автору или соавторам.
		// @Tags games
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 200 {string} html "Страница игры"
		// @Failure 404 {object} map[string]interface{} "Игра не найдена"
		// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
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
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Router /games/new [get]
		// @Security JWT
		protected.GET("/new", gameHandler.NewForm)

		// @Summary Создание игры
		// @Description Создаёт новую игру как черновик. Автоматически назначает текущего пользователя автором.
		// @Tags games
		// @Accept multipart/form-data
		// @Produce html
		// @Param name formData string true "Название игры (3-100 символов)"
		// @Param description formData string true "Описание игры (10-2000 символов)"
		// @Param max_team_number formData int true "Максимальное количество команд (1-100)"
		// @Param visibility formData string true "Видимость: public или private"
		// @Param starts_at formData string false "Дата и время начала (RFC3339)"
		// @Param registration_deadline formData string false "Крайний срок регистрации"
		// @Param cover formData file false "Обложка игры (jpeg, png, webp, до 5 МБ)"
		// @Success 302 {string} string "Перенаправление на /games"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Router /games [post]
		// @Security JWT
		protected.POST("/new", gameHandler.Create)

		// @Summary Форма редактирования игры
		// @Description Возвращает HTML-страницу с формой для редактирования игры (доступно автору или контент-менеджеру)
		// @Tags games
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 200 {string} html "Форма редактирования игры"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Failure 404 {object} map[string]interface{} "Игра не найдена"
		// @Router /games/{id}/edit [get]
		// @Security JWT
		protected.GET("/:id/edit", gameHandler.EditForm)

		// @Summary Обновление игры
		// @Description Обновляет данные игры (доступно автору или контент-менеджеру)
		// @Tags games
		// @Accept multipart/form-data
		// @Produce html
		// @Param id path int true "ID игры"
		// @Param name formData string false "Название игры (3-100 символов)"
		// @Param description formData string false "Описание игры (10-2000 символов)"
		// @Param max_team_number formData int false "Максимальное количество команд (1-100)"
		// @Param visibility formData string false "Видимость: public или private"
		// @Param starts_at formData string false "Дата и время начала (RFC3339)"
		// @Param registration_deadline formData string false "Крайний срок регистрации"
		// @Param cover formData file false "Обложка игры (jpeg, png, webp, до 5 МБ)"
		// @Param delete_cover formData string false "Удалить обложку (1)"
		// @Success 302 {string} string "Перенаправление на /games/{id}"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
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
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав (только владелец)"
		// @Router /games/{id}/delete [post]
		// @Security JWT
		protected.POST("/:id/delete", gameHandler.Delete)

		// @Summary Публикация игры
		// @Description Публикует черновик игры (доступно автору или контент-менеджеру). Игра должна содержать хотя бы один уровень.
		// @Tags games
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 302 {string} string "Перенаправление на /games/{id}"
		// @Failure 400 {object} map[string]interface{} "Нет уровней или уже опубликована"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /games/{id}/publish [post]
		// @Security JWT
		protected.POST("/:id/publish", gameHandler.Publish)

		// @Summary Управление соавторами
		// @Description Отображает страницу управления соавторами игры (доступно владельцу)
		// @Tags coauthors
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 200 {string} html "Страница управления соавторами"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
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
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
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
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /games/{id}/co-authors/{user_id}/delete [post]
		// @Security JWT
		protected.POST("/:id/co-authors/:user_id/delete", gameHandler.RemoveCoAuthor)

		// @Summary Список заявок и прохождений
		// @Description Отображает все заявки и текущие прохождения игры (доступно автору или модератору)
		// @Tags passings
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 200 {string} html "Страница с прохождениями"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /games/{id}/passings [get]
		// @Security JWT
		protected.GET("/:id/passings", gameHandler.ListPassings)

		// @Summary Изменение статуса заявки
		// @Description Принимает или отклоняет заявку команды на участие в игре (доступно автору или модератору)
		// @Tags passings
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID игры"
		// @Param passing_id path int true "ID заявки"
		// @Param status formData string true "Новый статус (accepted / rejected)"
		// @Success 302 {string} string "Перенаправление на /games/{id}/passings"
		// @Failure 400 {object} map[string]interface{} "Недопустимый статус"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /games/{id}/passings/{passing_id}/status [post]
		// @Security JWT
		protected.POST("/:id/passings/:passing_id/status", gameHandler.UpdatePassingStatus)

		// @Summary Запуск игры
		// @Description Запускает игру для конкретного прохождения (доступно капитану команды или автору/модератору)
		// @Tags passings
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID игры"
		// @Param passing_id path int true "ID прохождения"
		// @Success 302 {string} string "Перенаправление на /games/{id}/monitor"
		// @Failure 400 {object} map[string]interface{} "Игра не принята или уже началась"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /games/{id}/passings/{passing_id}/start [post]
		// @Security JWT
		protected.POST("/:id/passings/:passing_id/start", gameHandler.StartGame)

		// @Summary Подача заявки на игру (форма)
		// @Description Возвращает страницу выбора команды для подачи заявки на участие в игре
		// @Tags passings
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 200 {string} html "Форма подачи заявки"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Router /games/{id}/apply [get]
		// @Security JWT
		protected.GET("/:id/apply", gameHandler.ApplyForm)

		// @Summary Подача заявки на игру
		// @Description Подаёт заявку от команды на участие в игре (доступно капитану команды)
		// @Tags passings
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID игры"
		// @Param team_id formData uint true "ID команды"
		// @Success 302 {string} string "Перенаправление на /games/{id}"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав (только капитан)"
		// @Router /games/{id}/apply [post]
		// @Security JWT
		protected.POST("/:id/apply", gameHandler.Apply)

		// @Summary Симуляция прохождения
		// @Description Запускает симуляцию игры для проверки логики (доступно автору или соавтору)
		// @Tags games
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 200 {string} html "Результаты симуляции"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /games/{id}/simulate [get]
		// @Security JWT
		protected.GET("/:id/simulate", gameHandler.Simulate)

		// @Summary Настройки игры
		// @Description Отображает страницу с настройками игры (доступно автору или контент-менеджеру)
		// @Tags games
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 200 {string} html "Страница настроек"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Failure 404 {object} map[string]interface{} "Игра не найдена"
		// @Router /games/{id}/settings [get]
		// @Security JWT
		protected.GET("/:id/settings", gameHandler.SettingsPage)

		// @Summary Сохранение настроек
		// @Description Сохраняет настройки игры (доступно автору или контент-менеджеру)
		// @Tags games
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID игры"
		// @Param allow_hints formData bool false "Разрешить подсказки"
		// @Param hint_penalty_seconds formData int false "Штраф за подсказку (секунд)"
		// @Param max_hints formData int false "Максимальное количество подсказок"
		// @Param per_level_time_limit formData int false "Лимит времени на уровень (минут, 0 - без ограничения)"
		// @Param hide_answers_until_finished formData bool false "Скрывать ответы до финиша"
		// @Param auto_start formData bool false "Автоматический старт в указанное время"
		// @Success 302 {string} string "Перенаправление на /games/{id}/settings"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /games/{id}/settings [post]
		// @Security JWT
		protected.POST("/:id/settings", gameHandler.SaveSettings)

		// @Summary Тестовые прохождения
		// @Description Отображает страницу управления тестовыми прохождениями игры (доступно автору или соавтору)
		// @Tags games
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 200 {string} html "Страница тестовых прохождений"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /games/{id}/test [get]
		// @Security JWT
		protected.GET("/:id/test", gameHandler.TestPage)

		// @Summary Фотогалерея игры
		// @Description Отображает страницу с фотографиями игры
		// @Tags games
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 200 {string} html "Страница фотогалереи"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Router /games/{id}/photos [get]
		// @Security JWT
		protected.GET("/:id/photos", gameHandler.PhotosPage)

		// @Summary Загрузка фото
		// @Description Загружает новое фото в галерею игры (до 10 МБ, поддерживаются JPEG, PNG, WebP)
		// @Tags games
		// @Accept multipart/form-data
		// @Produce json
		// @Param id path int true "ID игры"
		// @Param photo formData file true "Файл фотографии"
		// @Success 200 {object} map[string]interface{} "Загруженное фото"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Router /games/{id}/photos [post]
		// @Security JWT
		protected.POST("/:id/photos", gameHandler.UploadPhoto)

		// @Summary Удаление фото
		// @Description Удаляет фото из галереи (доступно автору фото или автору/соавтору игры)
		// @Tags games
		// @Produce json
		// @Param id path int true "ID игры"
		// @Param photo_id path int true "ID фото"
		// @Success 200 {object} map[string]interface{} "Статус удаления"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
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
	// @Summary Страница прохождения уровня
	// @Description Отображает текущий уровень для команды во время игры. Показывает вопрос, подсказки, историю попыток и таймер.
	// @Tags gameplay
	// @Produce html
	// @Param passing_id path int true "ID прохождения"
	// @Success 200 {string} html "Страница уровня"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 403 {object} map[string]interface{} "Доступ запрещён (не участник команды)"
	// @Failure 404 {object} map[string]interface{} "Уровень не найден"
	// @Router /game/{passing_id} [get]
	// @Security JWT
	r.GET("/game/:passing_id", handler.ShowGame)

	// @Summary Отправка кода
	// @Description Отправляет текстовый код для проверки на текущем уровне с ограничением частоты запросов (10 попыток за минуту на пользователя)
	// @Tags gameplay
	// @Accept x-www-form-urlencoded
	// @Produce html
	// @Param passing_id path int true "ID прохождения"
	// @Param code formData string true "Код для проверки (1-10000 символов)"
	// @Success 302 {string} string "Перенаправление на /game/{passing_id}"
	// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
	// @Failure 429 {object} map[string]interface{} "Слишком частый ввод кодов"
	// @Router /game/{passing_id}/submit [post]
	// @Security JWT
	r.POST("/game/:passing_id/submit", middleware.CodeSubmissionRateLimit(1*time.Minute, 10), handler.SubmitCode)

	// @Summary Использование подсказки
	// @Description Запрашивает подсказку для текущего уровня (увеличивает штрафное время). Лимит подсказок задаётся в настройках игры.
	// @Tags gameplay
	// @Accept x-www-form-urlencoded
	// @Produce html
	// @Param passing_id path int true "ID прохождения"
	// @Success 302 {string} string "Перенаправление на /game/{passing_id}"
	// @Failure 400 {object} map[string]interface{} "Подсказки запрещены или лимит исчерпан"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
	// @Router /game/{passing_id}/hint [post]
	// @Security JWT
	r.POST("/game/:passing_id/hint", handler.UseHint)

	// @Summary Загрузка файла ответа
	// @Description Загружает файл в качестве ответа на текущий уровень (до 10 МБ, поддерживаются JPEG, PNG, GIF, PDF, TXT)
	// @Tags gameplay
	// @Accept multipart/form-data
	// @Produce html
	// @Param passing_id path int true "ID прохождения"
	// @Param answer_file formData file true "Файл ответа"
	// @Success 302 {string} string "Перенаправление на /game/{passing_id}"
	// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
	// @Router /game/{passing_id}/file [post]
	// @Security JWT
	r.POST("/game/:passing_id/file", handler.SubmitFile)

	// @Summary Подтверждение ответа (только для чёрного ящика)
	// @Description Автор игры подтверждает ответ команды на уровне "чёрный ящик"
	// @Tags gameplay
	// @Accept x-www-form-urlencoded
	// @Produce html
	// @Param passing_id path int true "ID прохождения"
	// @Success 302 {string} string "Перенаправление на /games/{game_id}/monitor"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 403 {object} map[string]interface{} "Только автор может подтвердить"
	// @Router /game/{passing_id}/accept [post]
	// @Security JWT
	r.POST("/game/:passing_id/accept", handler.AcceptAnswer)

	// @Summary Страница тестового прохождения
	// @Description Отображает текущий уровень в тестовом режиме (виден автору или соавтору)
	// @Tags testing
	// @Produce html
	// @Param passing_id path int true "ID прохождения"
	// @Success 200 {string} html "Страница тестового уровня"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
	// @Failure 404 {object} map[string]interface{} "Уровень не найден"
	// @Router /testing/{passing_id} [get]
	// @Security JWT
	r.GET("/testing/:passing_id", handler.ShowTestGame)

	// @Summary Отправка кода в тестовом режиме
	// @Description Отправляет код для проверки в тестовом режиме (всегда успешно) с ограничением частоты (10 попыток за минуту на пользователя)
	// @Tags testing
	// @Accept x-www-form-urlencoded
	// @Produce html
	// @Param passing_id path int true "ID прохождения"
	// @Param code formData string true "Код для проверки (1-10000 символов)"
	// @Success 302 {string} string "Перенаправление на /testing/{passing_id}"
	// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
	// @Failure 429 {object} map[string]interface{} "Слишком частый ввод кодов"
	// @Router /testing/{passing_id}/submit [post]
	// @Security JWT
	r.POST("/testing/:passing_id/submit", middleware.CodeSubmissionRateLimit(1*time.Minute, 10), handler.SubmitTestCode)

	// @Summary Пропуск уровня в тестовом режиме
	// @Description Пропускает текущий уровень в тестовом режиме (доступно автору или соавтору)
	// @Tags testing
	// @Accept x-www-form-urlencoded
	// @Produce html
	// @Param passing_id path int true "ID прохождения"
	// @Success 302 {string} string "Перенаправление на /testing/{passing_id}"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
	// @Router /testing/{passing_id}/skip [post]
	// @Security JWT
	r.POST("/testing/:passing_id/skip", handler.SkipTestLevel)
}
