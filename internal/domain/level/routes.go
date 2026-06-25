// internal/domain/level/routes.go
package level

import (
	"net/http"
	"strconv"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(
	r *gin.Engine,
	levelService *LevelService,
	questionService *QuestionService,
	answerService *AnswerService,
	localStorage storage.FileStorage,
	hub *ws.RoomHub,
	cfg *config.Config,
	authorizer middleware.GameAuthorizer,
	activeGameManager ActiveGameManager,
	authService *user.AuthService,
) {
	protected := r.Group("/games/:game_id/levels")
	protected.Use(middleware.AuthRequired(authService))

	// @Summary Список уровней игры
	// @Description Возвращает HTML-страницу со списком всех уровней игры
	// @Tags levels
	// @Produce html
	// @Param game_id path int true "ID игры"
	// @Success 200 {string} html "Страница со списком уровней"
	// @Failure 403 {object} map[string]interface{} "Нет прав доступа"
	// @Router /games/{game_id}/levels [get]
	// @Security JWT
	protected.GET("/", func(c *gin.Context) {
		gameID, _ := strconv.Atoi(c.Param("game_id"))
		levels, err := levelService.ListByGame(c.Request.Context(), uint(gameID))
		if err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		c.HTML(http.StatusOK, "levels_list.html", gin.H{
			"title":  "Уровни",
			"levels": levels,
			"gameID": gameID,
		})
	})

	// @Summary Форма создания уровня
	// @Description Возвращает HTML-страницу с формой для создания нового уровня
	// @Tags levels
	// @Produce html
	// @Param game_id path int true "ID игры"
	// @Success 200 {string} html "Форма создания уровня"
	// @Router /games/{game_id}/levels/create [get]
	// @Security JWT
	protected.GET("/create", func(c *gin.Context) {
		gameID, _ := strconv.Atoi(c.Param("game_id"))
		c.HTML(http.StatusOK, "level_create.html", gin.H{
			"title":  "Создать уровень",
			"gameID": gameID,
			"csrf":   c.GetString("csrf"),
		})
	})

	// @Summary Создание уровня
	// @Description Создаёт новый уровень в игре
	// @Tags levels
	// @Accept x-www-form-urlencoded
	// @Produce html
	// @Param game_id path int true "ID игры"
	// @Param name formData string true "Название уровня"
	// @Param description formData string false "Описание"
	// @Param position formData int false "Позиция"
	// @Param type formData string false "Тип уровня"
	// @Param parent_id formData int false "ID родительского уровня"
	// @Param group_id formData int false "ID группы"
	// @Param min_children formData int false "Минимальное количество детей"
	// @Param requires_confirmation formData bool false "Требуется подтверждение"
	// @Param latitude formData number false "Широта"
	// @Param longitude formData number false "Долгота"
	// @Success 302 {string} string "Перенаправление на /games/{game_id}/levels"
	// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
	// @Router /games/{game_id}/levels [post]
	// @Security JWT
	protected.POST("/create", func(c *gin.Context) {
		gameID, _ := strconv.Atoi(c.Param("game_id"))
		var level Level
		if err := c.ShouldBind(&level); err != nil {
			c.HTML(http.StatusBadRequest, "level_create.html", gin.H{"error": err.Error()})
			return
		}
		if err := levelService.Create(c.Request.Context(), uint(gameID), &level, c.GetUint("user_id")); err != nil {
			c.HTML(http.StatusBadRequest, "level_create.html", gin.H{"error": err.Error()})
			return
		}
		c.Redirect(http.StatusFound, "/games/"+strconv.Itoa(gameID)+"/levels")
	})

	// @Summary Детали уровня
	// @Description Отображает подробную информацию об уровне
	// @Tags levels
	// @Produce html
	// @Param game_id path int true "ID игры"
	// @Param level_id path int true "ID уровня"
	// @Success 200 {string} html "Страница уровня"
	// @Failure 404 {object} map[string]interface{} "Уровень не найден"
	// @Router /games/{game_id}/levels/{level_id} [get]
	// @Security JWT
	protected.GET("/:level_id", func(c *gin.Context) {
		levelID, _ := strconv.Atoi(c.Param("level_id"))
		level, err := levelService.GetByID(c.Request.Context(), uint(levelID))
		if err != nil {
			c.String(http.StatusNotFound, err.Error())
			return
		}
		c.HTML(http.StatusOK, "level_detail.html", gin.H{
			"title": level.Name,
			"level": level,
		})
	})

	// @Summary Форма редактирования уровня
	// @Description Возвращает HTML-страницу с формой для редактирования уровня
	// @Tags levels
	// @Produce html
	// @Param game_id path int true "ID игры"
	// @Param level_id path int true "ID уровня"
	// @Success 200 {string} html "Форма редактирования уровня"
	// @Failure 404 {object} map[string]interface{} "Уровень не найден"
	// @Router /games/{game_id}/levels/{level_id}/edit [get]
	// @Security JWT
	protected.GET("/:level_id/edit", func(c *gin.Context) {
		levelID, _ := strconv.Atoi(c.Param("level_id"))
		level, err := levelService.GetByID(c.Request.Context(), uint(levelID))
		if err != nil {
			c.String(http.StatusNotFound, err.Error())
			return
		}
		c.HTML(http.StatusOK, "level_edit.html", gin.H{
			"title": "Редактировать уровень",
			"level": level,
			"csrf":  c.GetString("csrf"),
		})
	})

	// @Summary Обновление уровня
	// @Description Обновляет данные уровня
	// @Tags levels
	// @Accept x-www-form-urlencoded
	// @Produce html
	// @Param game_id path int true "ID игры"
	// @Param level_id path int true "ID уровня"
	// @Param name formData string false "Название уровня"
	// @Param description formData string false "Описание"
	// @Param position formData int false "Позиция"
	// @Param type formData string false "Тип уровня"
	// @Param parent_id formData int false "ID родительского уровня"
	// @Param group_id formData int false "ID группы"
	// @Param min_children formData int false "Минимальное количество детей"
	// @Param requires_confirmation formData bool false "Требуется подтверждение"
	// @Param latitude formData number false "Широта"
	// @Param longitude formData number false "Долгота"
	// @Success 302 {string} string "Перенаправление на /games/{game_id}/levels/{level_id}"
	// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
	// @Router /games/{game_id}/levels/{level_id} [put]
	// @Security JWT
	protected.POST("/:level_id/edit", func(c *gin.Context) {
		levelID, _ := strconv.Atoi(c.Param("level_id"))
		var updated Level
		if err := c.ShouldBind(&updated); err != nil {
			c.HTML(http.StatusBadRequest, "level_edit.html", gin.H{"error": err.Error()})
			return
		}
		if err := levelService.Update(c.Request.Context(), uint(levelID), &updated, c.GetUint("user_id")); err != nil {
			c.HTML(http.StatusBadRequest, "level_edit.html", gin.H{"error": err.Error()})
			return
		}
		c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/levels/"+strconv.Itoa(levelID))
	})

	// @Summary Удаление уровня
	// @Description Удаляет уровень из игры (доступно автору или контент-менеджеру)
	// @Tags levels
	// @Accept x-www-form-urlencoded
	// @Produce html
	// @Param game_id path int true "ID игры"
	// @Param level_id path int true "ID уровня"
	// @Success 302 {string} string "Перенаправление на /games/{game_id}/levels"
	// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
	// @Router /games/{game_id}/levels/{level_id} [delete]
	// @Security JWT
	protected.POST("/:level_id/delete", func(c *gin.Context) {
		levelID, _ := strconv.Atoi(c.Param("level_id"))
		gameID, _ := strconv.Atoi(c.Param("game_id"))
		if err := levelService.DeleteFromActiveGame(c.Request.Context(), uint(gameID), uint(levelID), c.GetUint("user_id")); err != nil {
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		c.Redirect(http.StatusFound, "/games/"+strconv.Itoa(gameID)+"/levels")
	})

	// @Summary Дублирование уровня
	// @Description Создаёт копию уровня
	// @Tags levels
	// @Accept x-www-form-urlencoded
	// @Produce html
	// @Param game_id path int true "ID игры"
	// @Param level_id path int true "ID уровня"
	// @Success 302 {string} string "Перенаправление на /games/{game_id}/levels/{new_level_id}"
	// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
	// @Router /games/{game_id}/levels/{level_id}/duplicate [post]
	// @Security JWT
	protected.POST("/:level_id/duplicate", func(c *gin.Context) {
		levelID, _ := strconv.Atoi(c.Param("level_id"))
		newLevel, err := levelService.Duplicate(c.Request.Context(), uint(levelID), c.GetUint("user_id"))
		if err != nil {
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/levels/"+strconv.Itoa(int(newLevel.ID)))
	})

	// @Summary Перемещение уровня
	// @Description Изменяет позицию уровня (вверх/вниз)
	// @Tags levels
	// @Accept x-www-form-urlencoded
	// @Produce html
	// @Param game_id path int true "ID игры"
	// @Param level_id path int true "ID уровня"
	// @Param direction formData string true "Направление (up/down)"
	// @Success 302 {string} string "Перенаправление на /games/{game_id}/levels"
	// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
	// @Router /games/{game_id}/levels/{level_id}/move [post]
	// @Security JWT
	protected.POST("/:level_id/move", func(c *gin.Context) {
		levelID, _ := strconv.Atoi(c.Param("level_id"))
		direction := c.PostForm("direction")
		if err := levelService.Move(c.Request.Context(), uint(levelID), direction, c.GetUint("user_id")); err != nil {
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/levels")
	})

	questionGroup := protected.Group("/:level_id/questions")
	{
		// @Summary Список вопросов уровня
		// @Description Возвращает HTML-страницу со списком всех вопросов уровня
		// @Tags questions
		// @Produce html
		// @Param game_id path int true "ID игры"
		// @Param level_id path int true "ID уровня"
		// @Success 200 {string} html "Страница со списком вопросов"
		// @Failure 403 {object} map[string]interface{} "Нет прав доступа"
		// @Router /games/{game_id}/levels/{level_id}/questions [get]
		// @Security JWT
		questionGroup.GET("/", func(c *gin.Context) {
			levelID, _ := strconv.Atoi(c.Param("level_id"))
			questions, err := questionService.ListByLevel(c.Request.Context(), uint(levelID))
			if err != nil {
				c.String(http.StatusInternalServerError, err.Error())
				return
			}
			c.HTML(http.StatusOK, "questions_list.html", gin.H{
				"title":     "Вопросы",
				"questions": questions,
				"levelID":   levelID,
			})
		})

		// @Summary Форма создания вопроса
		// @Description Возвращает HTML-страницу с формой для создания нового вопроса
		// @Tags questions
		// @Produce html
		// @Param game_id path int true "ID игры"
		// @Param level_id path int true "ID уровня"
		// @Success 200 {string} html "Форма создания вопроса"
		// @Router /games/{game_id}/levels/{level_id}/questions/create [get]
		// @Security JWT
		questionGroup.GET("/create", func(c *gin.Context) {
			levelID, _ := strconv.Atoi(c.Param("level_id"))
			c.HTML(http.StatusOK, "question_create.html", gin.H{
				"title":   "Создать вопрос",
				"levelID": levelID,
				"csrf":    c.GetString("csrf"),
			})
		})

		// @Summary Создание вопроса
		// @Description Создаёт новый вопрос в уровне
		// @Tags questions
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param game_id path int true "ID игры"
		// @Param level_id path int true "ID уровня"
		// @Param text formData string true "Текст вопроса"
		// @Param hint formData string false "Подсказка"
		// @Success 302 {string} string "Перенаправление на /games/{game_id}/levels/{level_id}/questions"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Router /games/{game_id}/levels/{level_id}/questions [post]
		// @Security JWT
		questionGroup.POST("/create", func(c *gin.Context) {
			levelID, _ := strconv.Atoi(c.Param("level_id"))
			var question Question
			if err := c.ShouldBind(&question); err != nil {
				c.HTML(http.StatusBadRequest, "question_create.html", gin.H{"error": err.Error()})
				return
			}
			if err := questionService.Create(c.Request.Context(), uint(levelID), &question, c.GetUint("user_id")); err != nil {
				c.HTML(http.StatusBadRequest, "question_create.html", gin.H{"error": err.Error()})
				return
			}
			c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/levels/"+c.Param("level_id")+"/questions")
		})

		// @Summary Форма редактирования вопроса
		// @Description Возвращает HTML-страницу с формой для редактирования вопроса
		// @Tags questions
		// @Produce html
		// @Param game_id path int true "ID игры"
		// @Param level_id path int true "ID уровня"
		// @Param question_id path int true "ID вопроса"
		// @Success 200 {string} html "Форма редактирования вопроса"
		// @Failure 404 {object} map[string]interface{} "Вопрос не найден"
		// @Router /games/{game_id}/levels/{level_id}/questions/{question_id}/edit [get]
		// @Security JWT
		questionGroup.GET("/:question_id/edit", func(c *gin.Context) {
			questionID, _ := strconv.Atoi(c.Param("question_id"))
			question, err := questionService.GetByID(c.Request.Context(), uint(questionID))
			if err != nil {
				c.String(http.StatusNotFound, err.Error())
				return
			}
			c.HTML(http.StatusOK, "question_edit.html", gin.H{
				"title":    "Редактировать вопрос",
				"question": question,
				"csrf":     c.GetString("csrf"),
			})
		})

		// @Summary Обновление вопроса
		// @Description Обновляет данные вопроса
		// @Tags questions
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param game_id path int true "ID игры"
		// @Param level_id path int true "ID уровня"
		// @Param question_id path int true "ID вопроса"
		// @Param text formData string true "Текст вопроса"
		// @Param hint formData string false "Подсказка"
		// @Success 302 {string} string "Перенаправление на /games/{game_id}/levels/{level_id}/questions"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Router /games/{game_id}/levels/{level_id}/questions/{question_id} [put]
		// @Security JWT
		questionGroup.POST("/:question_id/edit", func(c *gin.Context) {
			questionID, _ := strconv.Atoi(c.Param("question_id"))
			var updated Question
			if err := c.ShouldBind(&updated); err != nil {
				c.HTML(http.StatusBadRequest, "question_edit.html", gin.H{"error": err.Error()})
				return
			}
			if err := questionService.Update(c.Request.Context(), uint(questionID), &updated, c.GetUint("user_id")); err != nil {
				c.HTML(http.StatusBadRequest, "question_edit.html", gin.H{"error": err.Error()})
				return
			}
			c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/levels/"+c.Param("level_id")+"/questions")
		})

		// @Summary Удаление вопроса
		// @Description Удаляет вопрос из уровня
		// @Tags questions
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param game_id path int true "ID игры"
		// @Param level_id path int true "ID уровня"
		// @Param question_id path int true "ID вопроса"
		// @Success 302 {string} string "Перенаправление на /games/{game_id}/levels/{level_id}/questions"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /games/{game_id}/levels/{level_id}/questions/{question_id} [delete]
		// @Security JWT
		questionGroup.POST("/:question_id/delete", func(c *gin.Context) {
			questionID, _ := strconv.Atoi(c.Param("question_id"))
			if err := questionService.Delete(c.Request.Context(), uint(questionID), c.GetUint("user_id")); err != nil {
				c.String(http.StatusBadRequest, err.Error())
				return
			}
			c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/levels/"+c.Param("level_id")+"/questions")
		})

		answerGroup := questionGroup.Group("/:question_id/answers")
		{
			// @Summary Список ответов
			// @Description Возвращает HTML-страницу со списком всех ответов на вопрос
			// @Tags answers
			// @Produce html
			// @Param game_id path int true "ID игры"
			// @Param level_id path int true "ID уровня"
			// @Param question_id path int true "ID вопроса"
			// @Success 200 {string} html "Страница со списком ответов"
			// @Failure 403 {object} map[string]interface{} "Нет прав доступа"
			// @Router /games/{game_id}/levels/{level_id}/questions/{question_id}/answers [get]
			// @Security JWT
			answerGroup.GET("/", func(c *gin.Context) {
				questionID, _ := strconv.Atoi(c.Param("question_id"))
				answers, err := answerService.ListByQuestion(c.Request.Context(), uint(questionID))
				if err != nil {
					c.String(http.StatusInternalServerError, err.Error())
					return
				}
				c.HTML(http.StatusOK, "answers_list.html", gin.H{
					"title":      "Ответы",
					"answers":    answers,
					"questionID": questionID,
				})
			})

			// @Summary Создание ответа
			// @Description Создаёт новый вариант ответа для вопроса
			// @Tags answers
			// @Accept x-www-form-urlencoded
			// @Produce html
			// @Param game_id path int true "ID игры"
			// @Param level_id path int true "ID уровня"
			// @Param question_id path int true "ID вопроса"
			// @Param code formData string true "Код ответа"
			// @Success 302 {string} string "Перенаправление на /games/{game_id}/levels/{level_id}/questions/{question_id}/answers"
			// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
			// @Router /games/{game_id}/levels/{level_id}/questions/{question_id}/answers [post]
			// @Security JWT
			answerGroup.POST("/create", func(c *gin.Context) {
				questionID, _ := strconv.Atoi(c.Param("question_id"))
				var answer Answer
				if err := c.ShouldBind(&answer); err != nil {
					c.String(http.StatusBadRequest, err.Error())
					return
				}
				if err := answerService.Create(c.Request.Context(), uint(questionID), &answer, c.GetUint("user_id")); err != nil {
					c.String(http.StatusBadRequest, err.Error())
					return
				}
				c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/levels/"+c.Param("level_id")+"/questions/"+c.Param("question_id")+"/answers")
			})

			// @Summary Удаление ответа
			// @Description Удаляет вариант ответа (должен остаться хотя бы один)
			// @Tags answers
			// @Accept x-www-form-urlencoded
			// @Produce html
			// @Param game_id path int true "ID игры"
			// @Param level_id path int true "ID уровня"
			// @Param question_id path int true "ID вопроса"
			// @Param answer_id path int true "ID ответа"
			// @Success 302 {string} string "Перенаправление на /games/{game_id}/levels/{level_id}/questions/{question_id}/answers"
			// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
			// @Router /games/{game_id}/levels/{level_id}/questions/{question_id}/answers/{answer_id} [delete]
			// @Security JWT
			answerGroup.POST("/:answer_id/delete", func(c *gin.Context) {
				answerID, _ := strconv.Atoi(c.Param("answer_id"))
				if err := answerService.Delete(c.Request.Context(), uint(answerID), c.GetUint("user_id")); err != nil {
					c.String(http.StatusBadRequest, err.Error())
					return
				}
				c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/levels/"+c.Param("level_id")+"/questions/"+c.Param("question_id")+"/answers")
			})
		}
	}
}
