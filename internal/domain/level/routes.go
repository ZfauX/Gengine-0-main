// internal/domain/level/routes.go
package level

import (
	"gengine-0/internal/config"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes регистрирует маршруты для уровней, вопросов и ответов.
// @tags levels
// @tags questions
// @tags answers
func RegisterRoutes(
	r *gin.RouterGroup,
	levelService *LevelService,
	questionService *QuestionService,
	answerService *AnswerService,
	localStorage storage.FileStorage,
	hub *ws.RoomHub,
	cfg *config.Config,
	authorizer middleware.GameAuthorizer,
	authService *user.AuthService,
) {
	handler := NewLevelHandler(
		levelService,
		questionService,
		answerService,
		localStorage,
		hub,
		cfg,
		authorizer,
		nil,
	)

	protected := r.Group("/games/:id/levels")
	protected.Use(middleware.AuthRequired(authService))

	// ========================================================================
	// УРОВНИ
	// ========================================================================

	// @Summary Список уровней игры
	// @Description Возвращает HTML-страницу со списком всех уровней игры (доступно автору или соавтору)
	// @Tags levels
	// @Produce html
	// @Param id path int true "ID игры"
	// @Success 200 {string} html "Страница со списком уровней"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 403 {object} map[string]interface{} "Нет прав доступа"
	// @Router /games/{id}/levels [get]
	// @Security JWT
	protected.GET("/", handler.ListByGame)

	// @Summary Форма создания уровня
	// @Description Возвращает HTML-страницу с формой для создания нового уровня (доступно автору или соавтору)
	// @Tags levels
	// @Produce html
	// @Param id path int true "ID игры"
	// @Success 200 {string} html "Форма создания уровня"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 403 {object} map[string]interface{} "Нет прав доступа"
	// @Router /games/{id}/levels/new [get]
	// @Security JWT
	protected.GET("/new", handler.NewForm)

	// @Summary Создание уровня
	// @Description Создаёт новый уровень в игре (доступно автору или соавтору)
	// @Tags levels
	// @Accept x-www-form-urlencoded
	// @Produce html
	// @Param id path int true "ID игры"
	// @Param name formData string true "Название уровня (2-100 символов)"
	// @Param description formData string false "Описание (до 5000 символов)"
	// @Param position formData int false "Позиция (порядок)"
	// @Param type formData string false "Тип уровня (single, checkpoint, parallel_group, blackbox, file_upload)"
	// @Param parent_id formData int false "ID родительского уровня"
	// @Param group_id formData int false "ID группы"
	// @Param min_children formData int false "Минимальное количество детей"
	// @Param requires_confirmation formData bool false "Требуется подтверждение"
	// @Param latitude formData number false "Широта (-90..90)"
	// @Param longitude formData number false "Долгота (-180..180)"
	// @Success 302 {string} string "Перенаправление на /games/{id}/levels"
	// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 403 {object} map[string]interface{} "Нет прав доступа"
	// @Router /games/{id}/levels [post]
	// @Security JWT
	protected.POST("/", handler.Create)

	// @Summary Страница просмотра уровня
	// @Description Отображает информацию об уровне, вопросах и ответах (доступно автору или соавтору)
	// @Tags levels
	// @Produce html
	// @Param id path int true "ID игры"
	// @Param level_id path int true "ID уровня"
	// @Success 200 {string} html "Страница просмотра уровня"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 403 {object} map[string]interface{} "Нет прав доступа"
	// @Failure 404 {object} map[string]interface{} "Уровень не найден"
	// @Router /games/{id}/levels/{level_id} [get]
	// @Security JWT
	protected.GET("/:level_id", handler.EditForm)

	// @Summary Форма редактирования уровня
	// @Description Возвращает HTML-страницу с формой для редактирования уровня (доступно автору или соавтору)
	// @Tags levels
	// @Produce html
	// @Param id path int true "ID игры"
	// @Param level_id path int true "ID уровня"
	// @Success 200 {string} html "Форма редактирования уровня"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 403 {object} map[string]interface{} "Нет прав доступа"
	// @Failure 404 {object} map[string]interface{} "Уровень не найден"
	// @Router /games/{id}/levels/{level_id}/edit [get]
	// @Security JWT
	protected.GET("/:level_id/edit", handler.EditForm)

	// @Summary Обновление уровня (через форму)
	// @Description Обновляет данные уровня (доступно автору или соавтору)
	// @Tags levels
	// @Accept x-www-form-urlencoded
	// @Produce html
	// @Param id path int true "ID игры"
	// @Param level_id path int true "ID уровня"
	// @Param name formData string false "Название уровня (2-100 символов)"
	// @Param description formData string false "Описание (до 5000 символов)"
	// @Param position formData int false "Позиция (порядок)"
	// @Param type formData string false "Тип уровня (single, checkpoint, parallel_group, blackbox, file_upload)"
	// @Param parent_id formData int false "ID родительского уровня"
	// @Param group_id formData int false "ID группы"
	// @Param min_children formData int false "Минимальное количество детей"
	// @Param requires_confirmation formData bool false "Требуется подтверждение"
	// @Param latitude formData number false "Широта (-90..90)"
	// @Param longitude formData number false "Долгота (-180..180)"
	// @Success 302 {string} string "Перенаправление на /games/{id}/levels"
	// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 403 {object} map[string]interface{} "Нет прав доступа"
	// @Router /games/{id}/levels/{level_id}/update [post]
	// @Security JWT
	protected.POST("/:level_id/update", handler.Update)

	// @Summary Обновление уровня (через AJAX/PUT)
	// @Description Обновляет данные уровня (доступно автору или соавтору)
	// @Tags levels
	// @Accept x-www-form-urlencoded
	// @Produce html
	// @Param id path int true "ID игры"
	// @Param level_id path int true "ID уровня"
	// @Param name formData string false "Название уровня (2-100 символов)"
	// @Param description formData string false "Описание (до 5000 символов)"
	// @Param position formData int false "Позиция (порядок)"
	// @Param type formData string false "Тип уровня (single, checkpoint, parallel_group, blackbox, file_upload)"
	// @Param parent_id formData int false "ID родительского уровня"
	// @Param group_id formData int false "ID группы"
	// @Param min_children formData int false "Минимальное количество детей"
	// @Param requires_confirmation formData bool false "Требуется подтверждение"
	// @Param latitude formData number false "Широта (-90..90)"
	// @Param longitude formData number false "Долгота (-180..180)"
	// @Success 302 {string} string "Перенаправление на /games/{id}/levels"
	// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 403 {object} map[string]interface{} "Нет прав доступа"
	// @Router /games/{id}/levels/{level_id}/edit [post]
	// @Security JWT
	protected.POST("/:level_id/edit", handler.Update)

	// @Summary Удаление уровня
	// @Description Удаляет уровень из игры (доступно автору или контент-менеджеру). Если игра активна, прогресс команд переключается на следующий уровень.
	// @Tags levels
	// @Accept x-www-form-urlencoded
	// @Produce html
	// @Param id path int true "ID игры"
	// @Param level_id path int true "ID уровня"
	// @Success 302 {string} string "Перенаправление на /games/{id}/levels"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
	// @Router /games/{id}/levels/{level_id}/delete [post]
	// @Security JWT
	protected.POST("/:level_id/delete", handler.Delete)

	// @Summary Дублирование уровня
	// @Description Создаёт копию уровня со всеми вопросами и ответами (доступно автору или соавтору)
	// @Tags levels
	// @Accept x-www-form-urlencoded
	// @Produce html
	// @Param id path int true "ID игры"
	// @Param level_id path int true "ID уровня"
	// @Success 302 {string} string "Перенаправление на /games/{id}/levels/{new_level_id}"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
	// @Router /games/{id}/levels/{level_id}/duplicate [post]
	// @Security JWT
	protected.POST("/:level_id/duplicate", handler.Duplicate)

	// @Summary Перемещение уровня
	// @Description Изменяет позицию уровня вверх или вниз (доступно автору или соавтору)
	// @Tags levels
	// @Accept x-www-form-urlencoded
	// @Produce html
	// @Param id path int true "ID игры"
	// @Param level_id path int true "ID уровня"
	// @Param direction formData string true "Направление (up/down)"
	// @Success 302 {string} string "Перенаправление на /games/{id}/levels"
	// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
	// @Router /games/{id}/levels/{level_id}/move [post]
	// @Security JWT
	protected.POST("/:level_id/move", handler.Move)

	// ========================================================================
	// ВОПРОСЫ
	// ========================================================================

	questions := protected.Group("/:level_id/questions")
	{
		// @Summary Список вопросов уровня
		// @Description Возвращает HTML-страницу со списком всех вопросов уровня (доступно автору или соавтору)
		// @Tags questions
		// @Produce html
		// @Param id path int true "ID игры"
		// @Param level_id path int true "ID уровня"
		// @Success 200 {string} html "Страница со списком вопросов"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Нет прав доступа"
		// @Router /games/{id}/levels/{level_id}/questions [get]
		// @Security JWT
		questions.GET("/", handler.ListQuestions)

		// @Summary Форма создания вопроса
		// @Description Возвращает HTML-страницу с формой для создания нового вопроса (доступно автору или соавтору)
		// @Tags questions
		// @Produce html
		// @Param id path int true "ID игры"
		// @Param level_id path int true "ID уровня"
		// @Success 200 {string} html "Форма создания вопроса"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Нет прав доступа"
		// @Router /games/{id}/levels/{level_id}/questions/new [get]
		// @Security JWT
		questions.GET("/new", handler.NewQuestionForm)

		// @Summary Создание вопроса
		// @Description Создаёт новый вопрос в уровне (доступно автору или соавтору)
		// @Tags questions
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID игры"
		// @Param level_id path int true "ID уровня"
		// @Param text formData string true "Текст вопроса (1-5000 символов)"
		// @Param hint formData string false "Подсказка (до 500 символов)"
		// @Success 302 {string} string "Перенаправление на /games/{id}/levels/{level_id}/questions"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Нет прав доступа"
		// @Router /games/{id}/levels/{level_id}/questions [post]
		// @Security JWT
		questions.POST("/", handler.CreateQuestion)

		// @Summary Форма редактирования вопроса
		// @Description Возвращает HTML-страницу с формой для редактирования вопроса (доступно автору или соавтору)
		// @Tags questions
		// @Produce html
		// @Param id path int true "ID игры"
		// @Param level_id path int true "ID уровня"
		// @Param question_id path int true "ID вопроса"
		// @Success 200 {string} html "Форма редактирования вопроса"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Нет прав доступа"
		// @Failure 404 {object} map[string]interface{} "Вопрос не найден"
		// @Router /games/{id}/levels/{level_id}/questions/{question_id}/edit [get]
		// @Security JWT
		questions.GET("/:question_id/edit", handler.EditQuestionForm)

		// @Summary Обновление вопроса
		// @Description Обновляет данные вопроса (доступно автору или соавтору)
		// @Tags questions
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID игры"
		// @Param level_id path int true "ID уровня"
		// @Param question_id path int true "ID вопроса"
		// @Param text formData string true "Текст вопроса (1-5000 символов)"
		// @Param hint formData string false "Подсказка (до 500 символов)"
		// @Success 302 {string} string "Перенаправление на /games/{id}/levels/{level_id}/questions"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Нет прав доступа"
		// @Router /games/{id}/levels/{level_id}/questions/{question_id}/edit [post]
		// @Security JWT
		questions.POST("/:question_id/edit", handler.UpdateQuestion)

		// @Summary Обновление вопроса (без /edit)
		// @Description Обновляет данные вопроса (доступно автору или соавтору)
		// @Tags questions
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID игры"
		// @Param level_id path int true "ID уровня"
		// @Param question_id path int true "ID вопроса"
		// @Param text formData string true "Текст вопроса (1-5000 символов)"
		// @Param hint formData string false "Подсказка (до 500 символов)"
		// @Success 302 {string} string "Перенаправление на /games/{id}/levels/{level_id}/questions"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Нет прав доступа"
		// @Router /games/{id}/levels/{level_id}/questions/{question_id} [post]
		// @Security JWT
		questions.POST("/:question_id", handler.UpdateQuestion)

		// @Summary Удаление вопроса
		// @Description Удаляет вопрос из уровня (доступно автору или соавтору)
		// @Tags questions
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID игры"
		// @Param level_id path int true "ID уровня"
		// @Param question_id path int true "ID вопроса"
		// @Success 302 {string} string "Перенаправление на /games/{id}/levels/{level_id}/questions"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /games/{id}/levels/{level_id}/questions/{question_id}/delete [post]
		// @Security JWT
		questions.POST("/:question_id/delete", handler.DeleteQuestion)

		// ====================================================================
		// ОТВЕТЫ
		// ====================================================================

		answers := questions.Group("/:question_id/answers")
		{
			// @Summary Список ответов
			// @Description Возвращает HTML-страницу со списком всех ответов на вопрос (доступно автору или соавтору)
			// @Tags answers
			// @Produce html
			// @Param id path int true "ID игры"
			// @Param level_id path int true "ID уровня"
			// @Param question_id path int true "ID вопроса"
			// @Success 200 {string} html "Страница со списком ответов"
			// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
			// @Failure 403 {object} map[string]interface{} "Нет прав доступа"
			// @Router /games/{id}/levels/{level_id}/questions/{question_id}/answers [get]
			// @Security JWT
			answers.GET("/", handler.ListAnswers)

			// @Summary Создание ответа
			// @Description Создаёт новый вариант ответа для вопроса (доступно автору или соавтору)
			// @Tags answers
			// @Accept x-www-form-urlencoded
			// @Produce html
			// @Param id path int true "ID игры"
			// @Param level_id path int true "ID уровня"
			// @Param question_id path int true "ID вопроса"
			// @Param code formData string true "Код ответа (1-1000 символов)"
			// @Success 302 {string} string "Перенаправление на /games/{id}/levels/{level_id}/questions/{question_id}/answers"
			// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
			// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
			// @Failure 403 {object} map[string]interface{} "Нет прав доступа"
			// @Router /games/{id}/levels/{level_id}/questions/{question_id}/answers [post]
			// @Security JWT
			answers.POST("/", handler.CreateAnswer)

			// @Summary Удаление ответа
			// @Description Удаляет вариант ответа (должен остаться хотя бы один) (доступно автору или соавтору)
			// @Tags answers
			// @Accept x-www-form-urlencoded
			// @Produce html
			// @Param id path int true "ID игры"
			// @Param level_id path int true "ID уровня"
			// @Param question_id path int true "ID вопроса"
			// @Param answer_id path int true "ID ответа"
			// @Success 302 {string} string "Перенаправление на /games/{id}/levels/{level_id}/questions/{question_id}/answers"
			// @Failure 400 {object} map[string]interface{} "Нельзя удалить последний ответ"
			// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
			// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
			// @Router /games/{id}/levels/{level_id}/questions/{question_id}/answers/{answer_id}/delete [post]
			// @Security JWT
			answers.POST("/:answer_id/delete", handler.DeleteAnswer)
		}
	}
}
