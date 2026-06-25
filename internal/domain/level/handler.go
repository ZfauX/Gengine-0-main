// internal/domain/level/handler.go
package level

import (
	"net/http"
	"strconv"

	"gengine-0/internal/config"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/gin-gonic/gin"
	csrf "github.com/utrack/gin-csrf"
	"gorm.io/gorm"
)

// ---------- Входные структуры ----------

type CreateLevelInput struct {
	Name                 string  `form:"name" binding:"required,min=2,max=100"`
	Description          string  `form:"description" binding:"max=5000"`
	Position             int     `form:"position" binding:"min=0"`
	Type                 string  `form:"type"`
	ParentID             *uint   `form:"parent_id"`
	GroupID              *uint   `form:"group_id"`
	MinChildren          int     `form:"min_children" binding:"min=0"`
	RequiresConfirmation bool    `form:"requires_confirmation"`
	Latitude             float64 `form:"latitude"`
	Longitude            float64 `form:"longitude"`
}

type UpdateLevelInput struct {
	Name                 string  `form:"name" binding:"min=2,max=100"`
	Description          string  `form:"description" binding:"max=5000"`
	Position             int     `form:"position" binding:"min=0"`
	Type                 string  `form:"type"`
	ParentID             *uint   `form:"parent_id"`
	GroupID              *uint   `form:"group_id"`
	MinChildren          int     `form:"min_children" binding:"min=0"`
	RequiresConfirmation bool    `form:"requires_confirmation"`
	Latitude             float64 `form:"latitude"`
	Longitude            float64 `form:"longitude"`
}

type CreateQuestionInput struct {
	Text string `form:"text" binding:"required"`
	Hint string `form:"hint"`
}

type UpdateQuestionInput struct {
	Text string `form:"text" binding:"required"`
	Hint string `form:"hint"`
}

type CreateAnswerInput struct {
	Code string `form:"code" binding:"required"`
}

// ---------- Обработчики ----------

type LevelHandler struct {
	levelService    *LevelService
	questionService *QuestionService
	answerService   *AnswerService
	storage         storage.FileStorage
	hub             *ws.RoomHub
	cfg             *config.Config
	authorizer      middleware.GameAuthorizer
	db              *gorm.DB
}

func NewLevelHandler(
	levelService *LevelService,
	questionService *QuestionService,
	answerService *AnswerService,
	storage storage.FileStorage,
	hub *ws.RoomHub,
	cfg *config.Config,
	authorizer middleware.GameAuthorizer,
	db *gorm.DB,
) *LevelHandler {
	return &LevelHandler{
		levelService:    levelService,
		questionService: questionService,
		answerService:   answerService,
		storage:         storage,
		hub:             hub,
		cfg:             cfg,
		authorizer:      authorizer,
		db:              db,
	}
}

// ----- Уровни -----

// ListByGame отображает список уровней игры.
// @Summary Список уровней игры
// @Description Возвращает HTML-страницу со списком всех уровней игры
// @Tags levels
// @Produce html
// @Param game_id path int true "ID игры"
// @Success 200 {string} html "Страница со списком уровней"
// @Failure 403 {object} map[string]interface{} "Нет прав доступа"
// @Router /games/{game_id}/levels [get]
// @Security JWT
func (h *LevelHandler) ListByGame(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("game_id"))
	userID := c.GetUint("userID")

	ok, err := h.authorizer.IsUserManager(uint(gameID), userID)
	if err != nil || !ok {
		c.HTML(http.StatusForbidden, "errors/403.html", nil)
		return
	}

	levels, err := h.levelService.ListByGame(c.Request.Context(), uint(gameID))
	if err != nil {
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "levels-list.html",
		"GameID":       gameID,
		"Levels":       levels,
		"csrf":         csrf.GetToken(c),
	})
}

// NewForm отображает форму создания уровня.
// @Summary Форма создания уровня
// @Description Возвращает HTML-страницу с формой для создания нового уровня
// @Tags levels
// @Produce html
// @Param game_id path int true "ID игры"
// @Success 200 {string} html "Форма создания уровня"
// @Router /games/{game_id}/levels/new [get]
// @Security JWT
func (h *LevelHandler) NewForm(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("game_id"))
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "levels-new.html",
		"GameID":       gameID,
		"csrf":         csrf.GetToken(c),
	})
}

// Create создаёт новый уровень.
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
func (h *LevelHandler) Create(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("game_id"))
	userID := c.GetUint("userID")

	var input CreateLevelInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "levels-new.html",
			"GameID":       gameID,
			"Error":        "Неверные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	level := &Level{
		Name:                 input.Name,
		Description:          input.Description,
		Position:             input.Position,
		Type:                 input.Type,
		ParentID:             input.ParentID,
		GroupID:              input.GroupID,
		MinChildren:          input.MinChildren,
		RequiresConfirmation: input.RequiresConfirmation,
		Latitude:             input.Latitude,
		Longitude:            input.Longitude,
	}

	if err := h.levelService.Create(c.Request.Context(), uint(gameID), level, userID); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "levels-new.html",
			"GameID":       gameID,
			"Error":        err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+strconv.Itoa(gameID)+"/levels")
}

// EditForm отображает форму редактирования уровня.
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
func (h *LevelHandler) EditForm(c *gin.Context) {
	levelID, _ := strconv.Atoi(c.Param("level_id"))
	userID := c.GetUint("userID")

	level, err := h.levelService.GetByID(c.Request.Context(), uint(levelID))
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}

	ok, err := h.authorizer.IsUserManager(level.GameID, userID)
	if err != nil || !ok {
		c.HTML(http.StatusForbidden, "errors/403.html", nil)
		return
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "levels-edit.html",
		"Level":        level,
		"csrf":         csrf.GetToken(c),
	})
}

// Update обновляет уровень.
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
func (h *LevelHandler) Update(c *gin.Context) {
	levelID, _ := strconv.Atoi(c.Param("level_id"))
	userID := c.GetUint("userID")

	var input UpdateLevelInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "levels-edit.html",
			"Error":        "Неверные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	updated := &Level{
		Name:                 input.Name,
		Description:          input.Description,
		Position:             input.Position,
		Type:                 input.Type,
		ParentID:             input.ParentID,
		GroupID:              input.GroupID,
		MinChildren:          input.MinChildren,
		RequiresConfirmation: input.RequiresConfirmation,
		Latitude:             input.Latitude,
		Longitude:            input.Longitude,
	}

	if err := h.levelService.Update(c.Request.Context(), uint(levelID), updated, userID); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "levels-edit.html",
			"Level":        updated,
			"Error":        err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/levels")
}

// Delete удаляет уровень (вызов через ActiveGameManager).
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
func (h *LevelHandler) Delete(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("game_id"))
	levelID, _ := strconv.Atoi(c.Param("level_id"))
	userID := c.GetUint("userID")

	if err := h.levelService.DeleteFromActiveGame(c.Request.Context(), uint(gameID), uint(levelID), userID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+strconv.Itoa(gameID)+"/levels")
}

// Duplicate дублирует уровень.
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
func (h *LevelHandler) Duplicate(c *gin.Context) {
	levelID, _ := strconv.Atoi(c.Param("level_id"))
	userID := c.GetUint("userID")

	newLevel, err := h.levelService.Duplicate(c.Request.Context(), uint(levelID), userID)
	if err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/levels/"+strconv.Itoa(int(newLevel.ID)))
}

// Move перемещает уровень.
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
func (h *LevelHandler) Move(c *gin.Context) {
	levelID, _ := strconv.Atoi(c.Param("level_id"))
	userID := c.GetUint("userID")
	direction := c.PostForm("direction")

	if err := h.levelService.Move(c.Request.Context(), uint(levelID), direction, userID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/levels")
}

// ----- Вопросы -----

// ListQuestions отображает список вопросов уровня.
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
func (h *LevelHandler) ListQuestions(c *gin.Context) {
	levelID, _ := strconv.Atoi(c.Param("level_id"))
	userID := c.GetUint("userID")

	level, err := h.levelService.GetByID(c.Request.Context(), uint(levelID))
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}

	ok, err := h.authorizer.IsUserManager(level.GameID, userID)
	if err != nil || !ok {
		c.HTML(http.StatusForbidden, "errors/403.html", nil)
		return
	}

	questions, err := h.questionService.ListByLevel(c.Request.Context(), uint(levelID))
	if err != nil {
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "questions-list.html",
		"LevelID":      levelID,
		"Questions":    questions,
		"csrf":         csrf.GetToken(c),
	})
}

// NewQuestionForm отображает форму создания вопроса.
// @Summary Форма создания вопроса
// @Description Возвращает HTML-страницу с формой для создания нового вопроса
// @Tags questions
// @Produce html
// @Param game_id path int true "ID игры"
// @Param level_id path int true "ID уровня"
// @Success 200 {string} html "Форма создания вопроса"
// @Router /games/{game_id}/levels/{level_id}/questions/new [get]
// @Security JWT
func (h *LevelHandler) NewQuestionForm(c *gin.Context) {
	levelID, _ := strconv.Atoi(c.Param("level_id"))
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "questions-new.html",
		"LevelID":      levelID,
		"csrf":         csrf.GetToken(c),
	})
}

// CreateQuestion создаёт новый вопрос.
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
func (h *LevelHandler) CreateQuestion(c *gin.Context) {
	levelID, _ := strconv.Atoi(c.Param("level_id"))
	userID := c.GetUint("userID")

	var input CreateQuestionInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "questions-new.html",
			"LevelID":      levelID,
			"Error":        "Неверные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	question := &Question{
		Text: input.Text,
		Hint: input.Hint,
	}

	if err := h.questionService.Create(c.Request.Context(), uint(levelID), question, userID); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "questions-new.html",
			"LevelID":      levelID,
			"Error":        err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/levels/"+c.Param("level_id")+"/questions")
}

// EditQuestionForm отображает форму редактирования вопроса.
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
func (h *LevelHandler) EditQuestionForm(c *gin.Context) {
	questionID, _ := strconv.Atoi(c.Param("question_id"))
	userID := c.GetUint("userID")

	question, err := h.questionService.GetByID(c.Request.Context(), uint(questionID))
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}

	level, err := h.levelService.GetByID(c.Request.Context(), question.LevelID)
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}

	ok, err := h.authorizer.IsUserManager(level.GameID, userID)
	if err != nil || !ok {
		c.HTML(http.StatusForbidden, "errors/403.html", nil)
		return
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "questions-edit.html",
		"Question":     question,
		"csrf":         csrf.GetToken(c),
	})
}

// UpdateQuestion обновляет вопрос.
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
func (h *LevelHandler) UpdateQuestion(c *gin.Context) {
	questionID, _ := strconv.Atoi(c.Param("question_id"))
	userID := c.GetUint("userID")

	var input UpdateQuestionInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "questions-edit.html",
			"Error":        "Неверные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	updated := &Question{
		Text: input.Text,
		Hint: input.Hint,
	}

	if err := h.questionService.Update(c.Request.Context(), uint(questionID), updated, userID); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "questions-edit.html",
			"Question":     updated,
			"Error":        err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/levels/"+c.Param("level_id")+"/questions")
}

// DeleteQuestion удаляет вопрос.
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
func (h *LevelHandler) DeleteQuestion(c *gin.Context) {
	questionID, _ := strconv.Atoi(c.Param("question_id"))
	userID := c.GetUint("userID")

	if err := h.questionService.Delete(c.Request.Context(), uint(questionID), userID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/levels/"+c.Param("level_id")+"/questions")
}

// ----- Ответы -----

// ListAnswers отображает список ответов вопроса.
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
func (h *LevelHandler) ListAnswers(c *gin.Context) {
	questionID, _ := strconv.Atoi(c.Param("question_id"))
	userID := c.GetUint("userID")

	question, err := h.questionService.GetByID(c.Request.Context(), uint(questionID))
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}

	level, err := h.levelService.GetByID(c.Request.Context(), question.LevelID)
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}

	ok, err := h.authorizer.IsUserManager(level.GameID, userID)
	if err != nil || !ok {
		c.HTML(http.StatusForbidden, "errors/403.html", nil)
		return
	}

	answers, err := h.answerService.ListByQuestion(c.Request.Context(), uint(questionID))
	if err != nil {
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "answers-list.html",
		"QuestionID":   questionID,
		"Answers":      answers,
		"csrf":         csrf.GetToken(c),
	})
}

// CreateAnswer создаёт новый ответ.
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
func (h *LevelHandler) CreateAnswer(c *gin.Context) {
	questionID, _ := strconv.Atoi(c.Param("question_id"))
	userID := c.GetUint("userID")

	var input CreateAnswerInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "answers-list.html",
			"QuestionID":   questionID,
			"Error":        "Неверные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	answer := &Answer{
		Code: input.Code,
	}

	if err := h.answerService.Create(c.Request.Context(), uint(questionID), answer, userID); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "answers-list.html",
			"QuestionID":   questionID,
			"Error":        err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/levels/"+c.Param("level_id")+"/questions/"+c.Param("question_id")+"/answers")
}

// DeleteAnswer удаляет ответ.
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
func (h *LevelHandler) DeleteAnswer(c *gin.Context) {
	answerID, _ := strconv.Atoi(c.Param("answer_id"))
	userID := c.GetUint("userID")

	if err := h.answerService.Delete(c.Request.Context(), uint(answerID), userID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/levels/"+c.Param("level_id")+"/questions/"+c.Param("question_id")+"/answers")
}
