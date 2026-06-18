// internal/domain/level/handler.go
package level

import (
	"net/http"
	"net/url"
	"strconv"

	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/utrack/gin-csrf"
	"github.com/gin-gonic/gin"
)

// ---------- Входные структуры для валидации ----------

type MoveLevelInput struct {
	Direction string `form:"direction" binding:"required,oneof=up down"`
}

// ---------- LevelHandler ----------

type LevelHandler struct {
	levelService    *LevelService
	questionService *QuestionService
	answerService   *AnswerService
	storage         storage.FileStorage
	hub             *ws.RoomHub
}

func NewLevelHandler(
	levelService *LevelService,
	questionService *QuestionService,
	answerService *AnswerService,
	storage storage.FileStorage,
	hub *ws.RoomHub,
) *LevelHandler {
	return &LevelHandler{
		levelService:    levelService,
		questionService: questionService,
		answerService:   answerService,
		storage:         storage,
		hub:             hub,
	}
}

// ListLevels отображает список уровней игры.
func (h *LevelHandler) ListLevels(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))
	levels, err := h.levelService.ListByGame(uint(gameID))
	if err != nil {
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}
	errorMsg := c.Query("error")
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "levels-list.html",
		"GameID":       gameID,
		"Levels":       levels,
		"Error":        errorMsg,
		"csrf":         csrf.GetToken(c),
	})
}

// NewLevelForm показывает форму создания уровня.
func (h *LevelHandler) NewLevelForm(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "levels-new.html",
		"GameID":       gameID,
		"csrf":         csrf.GetToken(c),
	})
}

// CreateLevel создаёт новый уровень.
func (h *LevelHandler) CreateLevel(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	var level Level
	if err := c.ShouldBind(&level); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "levels-new.html",
			"Error":        "Неверные данные: " + err.Error(),
			"GameID":       gameID,
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	if err := h.levelService.Create(uint(gameID), &level, userID); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "levels-new.html",
			"Error":        err.Error(),
			"GameID":       gameID,
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/levels")
}

// ShowLevel показывает уровень и его вопросы.
func (h *LevelHandler) ShowLevel(c *gin.Context) {
	levelID, _ := strconv.Atoi(c.Param("level_id"))
	level, err := h.levelService.GetByID(uint(levelID))
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "levels-show.html",
		"GameID":       c.Param("id"),
		"Level":        level,
		"csrf":         csrf.GetToken(c),
	})
}

// EditLevelForm показывает форму редактирования уровня.
func (h *LevelHandler) EditLevelForm(c *gin.Context) {
	levelID, _ := strconv.Atoi(c.Param("level_id"))
	level, err := h.levelService.GetByID(uint(levelID))
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "levels-edit.html",
		"GameID":       c.Param("id"),
		"Level":        level,
		"csrf":         csrf.GetToken(c),
	})
}

// UpdateLevel обновляет уровень.
func (h *LevelHandler) UpdateLevel(c *gin.Context) {
	levelID, _ := strconv.Atoi(c.Param("level_id"))
	userID := c.GetUint("userID")

	var updated Level
	if err := c.ShouldBind(&updated); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "levels-edit.html",
			"Error":        "Неверные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	if err := h.levelService.Update(uint(levelID), &updated, userID); err != nil {
		c.HTML(http.StatusForbidden, "levels/edit.html", gin.H{"Error": err.Error(), "csrf": csrf.GetToken(c)})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/levels")
}

// DeleteLevel удаляет уровень (мягкое удаление, если игра запущена — делегирует в game).
func (h *LevelHandler) DeleteLevel(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))
	levelID, _ := strconv.Atoi(c.Param("level_id"))
	userID := c.GetUint("userID")

	if err := h.levelService.DeleteFromActiveGame(uint(gameID), uint(levelID), userID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/levels")
}

// DuplicateLevel создаёт копию уровня со всеми вопросами и ответами.
func (h *LevelHandler) DuplicateLevel(c *gin.Context) {
	levelID, _ := strconv.Atoi(c.Param("level_id"))
	userID := c.GetUint("userID")

	_, err := h.levelService.Duplicate(uint(levelID), userID)
	if err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/levels")
}

// MoveLevel перемещает уровень вверх или вниз по порядку.
func (h *LevelHandler) MoveLevel(c *gin.Context) {
	levelID, _ := strconv.Atoi(c.Param("level_id"))
	userID := c.GetUint("userID")

	var input MoveLevelInput
	if err := c.ShouldBind(&input); err != nil {
		c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/levels?error="+url.QueryEscape("Неверные данные"))
		return
	}

	if err := h.levelService.Move(uint(levelID), input.Direction, userID); err != nil {
		c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/levels?error="+url.QueryEscape(err.Error()))
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/levels")
}

// ---------- QuestionHandler ----------

type QuestionHandler struct {
	questionService *QuestionService
}

func NewQuestionHandler(questionService *QuestionService) *QuestionHandler {
	return &QuestionHandler{questionService: questionService}
}

// ListQuestions отображает список вопросов уровня.
func (h *QuestionHandler) ListQuestions(c *gin.Context) {
	levelID, _ := strconv.Atoi(c.Param("level_id"))
	questions, err := h.questionService.ListByLevel(uint(levelID))
	if err != nil {
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "questions-list.html",
		"GameID":       c.Param("id"),
		"LevelID":      levelID,
		"Questions":    questions,
		"csrf":         csrf.GetToken(c),
	})
}

// NewQuestionForm показывает форму создания вопроса.
func (h *QuestionHandler) NewQuestionForm(c *gin.Context) {
	levelID, _ := strconv.Atoi(c.Param("level_id"))
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "questions-new.html",
		"GameID":       c.Param("id"),
		"LevelID":      levelID,
		"csrf":         csrf.GetToken(c),
	})
}

// CreateQuestion создаёт новый вопрос (и опционально ответы).
func (h *QuestionHandler) CreateQuestion(c *gin.Context) {
	levelID, _ := strconv.Atoi(c.Param("level_id"))
	userID := c.GetUint("userID")

	var question Question
	if err := c.ShouldBind(&question); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "questions-new.html",
			"Error":        "Неверные данные: " + err.Error(),
			"LevelID":      levelID,
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	if err := h.questionService.Create(uint(levelID), &question, userID); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "questions-new.html",
			"Error":        err.Error(),
			"LevelID":      levelID,
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/levels/"+c.Param("level_id")+"/questions")
}

// ShowQuestion показывает вопрос и его ответы.
func (h *QuestionHandler) ShowQuestion(c *gin.Context) {
	questionID, _ := strconv.Atoi(c.Param("question_id"))
	question, err := h.questionService.GetByID(uint(questionID))
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "questions-show.html",
		"GameID":       c.Param("id"),
		"LevelID":      c.Param("level_id"),
		"Question":     question,
		"csrf":         csrf.GetToken(c),
	})
}

// EditQuestionForm показывает форму редактирования вопроса.
func (h *QuestionHandler) EditQuestionForm(c *gin.Context) {
	questionID, _ := strconv.Atoi(c.Param("question_id"))
	question, err := h.questionService.GetByID(uint(questionID))
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "questions-edit.html",
		"GameID":       c.Param("id"),
		"LevelID":      c.Param("level_id"),
		"Question":     question,
		"csrf":         csrf.GetToken(c),
	})
}

// UpdateQuestion обновляет вопрос.
func (h *QuestionHandler) UpdateQuestion(c *gin.Context) {
	questionID, _ := strconv.Atoi(c.Param("question_id"))
	userID := c.GetUint("userID")

	var updated Question
	if err := c.ShouldBind(&updated); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "questions-edit.html",
			"Error":        "Неверные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	if err := h.questionService.Update(uint(questionID), &updated, userID); err != nil {
		c.HTML(http.StatusForbidden, "questions/edit.html", gin.H{"Error": err.Error(), "csrf": csrf.GetToken(c)})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/levels/"+c.Param("level_id")+"/questions")
}

// DeleteQuestion удаляет вопрос.
func (h *QuestionHandler) DeleteQuestion(c *gin.Context) {
	questionID, _ := strconv.Atoi(c.Param("question_id"))
	userID := c.GetUint("userID")

	if err := h.questionService.Delete(uint(questionID), userID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/levels/"+c.Param("level_id")+"/questions")
}

// ---------- AnswerHandler ----------

type AnswerHandler struct {
	answerService *AnswerService
}

func NewAnswerHandler(answerService *AnswerService) *AnswerHandler {
	return &AnswerHandler{answerService: answerService}
}

// Index отображает список ответов вопроса и форму создания нового ответа.
func (h *AnswerHandler) Index(c *gin.Context) {
	questionID, _ := strconv.Atoi(c.Param("question_id"))
	answers, err := h.answerService.ListByQuestion(uint(questionID))
	if err != nil {
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}
	newAnswer := Answer{QuestionID: uint(questionID)}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "answers-index.html",
		"GameID":       c.Param("id"),
		"LevelID":      c.Param("level_id"),
		"QuestionID":   questionID,
		"Answers":      answers,
		"NewAnswer":    newAnswer,
		"csrf":         csrf.GetToken(c),
	})
}

// Create создаёт новый ответ.
func (h *AnswerHandler) Create(c *gin.Context) {
	questionID, _ := strconv.Atoi(c.Param("question_id"))
	userID := c.GetUint("userID")

	var answer Answer
	if err := c.ShouldBind(&answer); err != nil {
		answers, _ := h.answerService.ListByQuestion(uint(questionID))
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "answers-index.html",
			"GameID":       c.Param("id"),
			"LevelID":      c.Param("level_id"),
			"QuestionID":   questionID,
			"Answers":      answers,
			"NewAnswer":    answer,
			"Error":        "Неверные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	if err := h.answerService.Create(uint(questionID), &answer, userID); err != nil {
		answers, _ := h.answerService.ListByQuestion(uint(questionID))
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "answers-index.html",
			"GameID":       c.Param("id"),
			"LevelID":      c.Param("level_id"),
			"QuestionID":   questionID,
			"Answers":      answers,
			"NewAnswer":    answer,
			"Error":        err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/levels/"+c.Param("level_id")+"/questions/"+c.Param("question_id")+"/answers")
}

// Delete удаляет ответ, если он не последний в вопросе.
func (h *AnswerHandler) Delete(c *gin.Context) {
	answerID, _ := strconv.Atoi(c.Param("answer_id"))
	userID := c.GetUint("userID")
	questionID, _ := strconv.Atoi(c.Param("question_id"))

	if err := h.answerService.Delete(uint(answerID), userID); err != nil {
		answers, _ := h.answerService.ListByQuestion(uint(questionID))
		newAnswer := Answer{QuestionID: uint(questionID)}
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "answers-index.html",
			"GameID":       c.Param("id"),
			"LevelID":      c.Param("level_id"),
			"QuestionID":   questionID,
			"Answers":      answers,
			"NewAnswer":    newAnswer,
			"Error":        err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/levels/"+c.Param("level_id")+"/questions/"+c.Param("question_id")+"/answers")
}