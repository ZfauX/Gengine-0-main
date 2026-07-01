// internal/domain/level/handler.go
package level

import (
	"errors"
	"net/http"
	"strconv"

	"gengine-0/internal/config"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/sanitize"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	csrf "github.com/utrack/gin-csrf"
	"gorm.io/gorm"
)

// ---------- Входные структуры для валидации ----------

// CreateLevelInput используется для создания уровня.
type CreateLevelInput struct {
	Name                 string  `form:"name" binding:"required,min=2,max=100"`
	Description          string  `form:"description" binding:"max=5000"`
	Position             int     `form:"position" binding:"min=0"`
	Type                 string  `form:"type" binding:"omitempty,oneof=standard group"`
	ParentID             *uint   `form:"parent_id"`
	GroupID              *uint   `form:"group_id"`
	MinChildren          int     `form:"min_children" binding:"min=0,max=100"`
	RequiresConfirmation bool    `form:"requires_confirmation"`
	Latitude             float64 `form:"latitude" binding:"omitempty,min=-90,max=90"`
	Longitude            float64 `form:"longitude" binding:"omitempty,min=-180,max=180"`
}

// UpdateLevelInput используется для обновления уровня.
type UpdateLevelInput struct {
	Name                 string  `form:"name" binding:"omitempty,min=2,max=100"`
	Description          string  `form:"description" binding:"max=5000"`
	Position             int     `form:"position" binding:"min=0"`
	Type                 string  `form:"type" binding:"omitempty,oneof=standard group"`
	ParentID             *uint   `form:"parent_id"`
	GroupID              *uint   `form:"group_id"`
	MinChildren          int     `form:"min_children" binding:"min=0,max=100"`
	RequiresConfirmation bool    `form:"requires_confirmation"`
	Latitude             float64 `form:"latitude" binding:"omitempty,min=-90,max=90"`
	Longitude            float64 `form:"longitude" binding:"omitempty,min=-180,max=180"`
}

// CreateQuestionInput используется для создания вопроса.
type CreateQuestionInput struct {
	Text string `form:"text" binding:"required,min=1,max=5000"`
	Hint string `form:"hint" binding:"max=500"`
}

// UpdateQuestionInput используется для обновления вопроса.
type UpdateQuestionInput struct {
	Text string `form:"text" binding:"required,min=1,max=5000"`
	Hint string `form:"hint" binding:"max=500"`
}

// CreateAnswerInput используется для создания ответа.
type CreateAnswerInput struct {
	Code string `form:"code" binding:"required,min=1,max=1000"`
}

// MoveLevelInput используется для перемещения уровня.
type MoveLevelInput struct {
	Direction string `form:"direction" binding:"required,oneof=up down"`
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
func (h *LevelHandler) ListByGame(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.HTML(http.StatusBadRequest, "errors-400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}
	userID := c.GetUint("userID")

	ok, err := h.authorizer.IsUserManager(uint(gameID), userID)
	if err != nil {
		log.Error().Err(err).Int("game_id", gameID).Uint("user", userID).Msg("ListByGame: failed to check manager")
		c.HTML(http.StatusInternalServerError, "errors-500.html", nil)
		return
	}
	if !ok {
		c.HTML(http.StatusForbidden, "errors-403.html", nil)
		return
	}

	levels, err := h.levelService.ListByGame(c.Request.Context(), uint(gameID))
	if err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("ListByGame: failed to list levels")
		c.HTML(http.StatusInternalServerError, "errors-500.html", nil)
		return
	}

	render.Page(c, http.StatusOK, "levels-list.html", gin.H{
		"GameID": gameID,
		"Levels": levels,
		"csrf":   csrf.GetToken(c),
	})
}

// NewForm отображает форму создания уровня.
func (h *LevelHandler) NewForm(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.HTML(http.StatusBadRequest, "errors-400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}
	render.Page(c, http.StatusOK, "levels-new.html", gin.H{
		"GameID": gameID,
		"csrf":   csrf.GetToken(c),
	})
}

// Create создаёт новый уровень.
func (h *LevelHandler) Create(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.HTML(http.StatusBadRequest, "errors-400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}
	userID := c.GetUint("userID")

	var input CreateLevelInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "levels-new.html", gin.H{
			"GameID": gameID,
			"Error":  "Неверные данные: " + err.Error(),
			"csrf":   csrf.GetToken(c),
		})
		return
	}

	level := &Level{
		Name:                 sanitize.StripHTML(input.Name),
		Description:          sanitize.StripHTML(input.Description),
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
		log.Error().Err(err).Int("game_id", gameID).Uint("user", userID).Msg("Create: failed to create level")
		render.Page(c, http.StatusInternalServerError, "levels-new.html", gin.H{
			"GameID": gameID,
			"Error":  err.Error(),
			"csrf":   csrf.GetToken(c),
		})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+strconv.Itoa(gameID)+"/levels")
}

// EditForm отображает форму редактирования уровня.
func (h *LevelHandler) EditForm(c *gin.Context) {
	levelID, err := strconv.Atoi(c.Param("level_id"))
	if err != nil || levelID <= 0 {
		c.HTML(http.StatusBadRequest, "errors-400.html", gin.H{"Error": "Неверный ID уровня"})
		return
	}
	userID := c.GetUint("userID")

	level, err := h.levelService.GetByID(c.Request.Context(), uint(levelID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.HTML(http.StatusNotFound, "errors-404.html", nil)
		} else {
			log.Error().Err(err).Int("level_id", levelID).Msg("EditForm: failed to get level")
			c.HTML(http.StatusInternalServerError, "errors-500.html", nil)
		}
		return
	}

	ok, err := h.authorizer.IsUserManager(level.GameID, userID)
	if err != nil {
		log.Error().Err(err).Uint("game_id", level.GameID).Uint("user", userID).Msg("EditForm: failed to check manager")
		c.HTML(http.StatusInternalServerError, "errors-500.html", nil)
		return
	}
	if !ok {
		c.HTML(http.StatusForbidden, "errors-403.html", nil)
		return
	}

	render.Page(c, http.StatusOK, "levels-edit.html", gin.H{
		"Level": level,
		"csrf":  csrf.GetToken(c),
	})
}

// Update обновляет уровень.
func (h *LevelHandler) Update(c *gin.Context) {
	levelID, err := strconv.Atoi(c.Param("level_id"))
	if err != nil || levelID <= 0 {
		c.HTML(http.StatusBadRequest, "errors-400.html", gin.H{"Error": "Неверный ID уровня"})
		return
	}
	userID := c.GetUint("userID")

	var input UpdateLevelInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "levels-edit.html", gin.H{
			"Error": "Неверные данные: " + err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	updated := &Level{
		Name:                 sanitize.StripHTML(input.Name),
		Description:          sanitize.StripHTML(input.Description),
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
		log.Error().Err(err).Int("level_id", levelID).Uint("user", userID).Msg("Update: failed to update level")
		render.Page(c, http.StatusInternalServerError, "levels-edit.html", gin.H{
			"Level": updated,
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	gameID, _ := strconv.Atoi(c.Param("id"))
	c.Redirect(http.StatusFound, "/games/"+strconv.Itoa(gameID)+"/levels")
}

// Delete удаляет уровень (вызов через ActiveGameManager).
func (h *LevelHandler) Delete(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.HTML(http.StatusBadRequest, "errors-400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}
	levelID, err := strconv.Atoi(c.Param("level_id"))
	if err != nil || levelID <= 0 {
		c.HTML(http.StatusBadRequest, "errors-400.html", gin.H{"Error": "Неверный ID уровня"})
		return
	}
	userID := c.GetUint("userID")

	if err := h.levelService.DeleteFromActiveGame(c.Request.Context(), uint(gameID), uint(levelID), userID); err != nil {
		c.HTML(http.StatusForbidden, "errors-403.html", gin.H{"Error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+strconv.Itoa(gameID)+"/levels")
}

// Duplicate дублирует уровень.
func (h *LevelHandler) Duplicate(c *gin.Context) {
	levelID, err := strconv.Atoi(c.Param("level_id"))
	if err != nil || levelID <= 0 {
		c.HTML(http.StatusBadRequest, "errors-400.html", gin.H{"Error": "Неверный ID уровня"})
		return
	}
	userID := c.GetUint("userID")

	newLevel, err := h.levelService.Duplicate(c.Request.Context(), uint(levelID), userID)
	if err != nil {
		log.Error().Err(err).Int("level_id", levelID).Uint("user", userID).Msg("Duplicate: failed to duplicate level")
		c.HTML(http.StatusForbidden, "errors-403.html", gin.H{"Error": err.Error()})
		return
	}

	gameID, _ := strconv.Atoi(c.Param("id"))
	c.Redirect(http.StatusFound, "/games/"+strconv.Itoa(gameID)+"/levels/"+strconv.Itoa(int(newLevel.ID)))
}

// Move перемещает уровень.
func (h *LevelHandler) Move(c *gin.Context) {
	levelID, err := strconv.Atoi(c.Param("level_id"))
	if err != nil || levelID <= 0 {
		c.HTML(http.StatusBadRequest, "errors-400.html", gin.H{"Error": "Неверный ID уровня"})
		return
	}
	userID := c.GetUint("userID")

	var input MoveLevelInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusBadRequest, "errors-400.html", gin.H{"Error": "Неверные данные: " + err.Error()})
		return
	}

	if err := h.levelService.Move(c.Request.Context(), uint(levelID), input.Direction, userID); err != nil {
		log.Error().Err(err).Int("level_id", levelID).Msg("Move: failed to move level")
		c.HTML(http.StatusForbidden, "errors-403.html", gin.H{"Error": err.Error()})
		return
	}

	gameID, _ := strconv.Atoi(c.Param("id"))
	c.Redirect(http.StatusFound, "/games/"+strconv.Itoa(gameID)+"/levels")
}

// ----- Вопросы -----

// ListQuestions отображает список вопросов уровня.
func (h *LevelHandler) ListQuestions(c *gin.Context) {
	levelID, err := strconv.Atoi(c.Param("level_id"))
	if err != nil || levelID <= 0 {
		c.HTML(http.StatusBadRequest, "errors-400.html", gin.H{"Error": "Неверный ID уровня"})
		return
	}
	userID := c.GetUint("userID")

	level, err := h.levelService.GetByID(c.Request.Context(), uint(levelID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.HTML(http.StatusNotFound, "errors-404.html", nil)
		} else {
			log.Error().Err(err).Int("level_id", levelID).Msg("ListQuestions: failed to get level")
			c.HTML(http.StatusInternalServerError, "errors-500.html", nil)
		}
		return
	}

	ok, err := h.authorizer.IsUserManager(level.GameID, userID)
	if err != nil {
		log.Error().Err(err).Uint("game_id", level.GameID).Uint("user", userID).Msg("ListQuestions: failed to check manager")
		c.HTML(http.StatusInternalServerError, "errors-500.html", nil)
		return
	}
	if !ok {
		c.HTML(http.StatusForbidden, "errors-403.html", nil)
		return
	}

	questions, err := h.questionService.ListByLevel(c.Request.Context(), uint(levelID))
	if err != nil {
		log.Error().Err(err).Int("level_id", levelID).Msg("ListQuestions: failed to list questions")
		c.HTML(http.StatusInternalServerError, "errors-500.html", nil)
		return
	}

	render.Page(c, http.StatusOK, "questions-list.html", gin.H{
		"LevelID":   levelID,
		"Questions": questions,
		"csrf":      csrf.GetToken(c),
	})
}

// NewQuestionForm отображает форму создания вопроса.
func (h *LevelHandler) NewQuestionForm(c *gin.Context) {
	levelID, err := strconv.Atoi(c.Param("level_id"))
	if err != nil || levelID <= 0 {
		c.HTML(http.StatusBadRequest, "errors-400.html", gin.H{"Error": "Неверный ID уровня"})
		return
	}
	render.Page(c, http.StatusOK, "questions-new.html", gin.H{
		"LevelID": levelID,
		"csrf":    csrf.GetToken(c),
	})
}

// CreateQuestion создаёт новый вопрос.
func (h *LevelHandler) CreateQuestion(c *gin.Context) {
	levelID, err := strconv.Atoi(c.Param("level_id"))
	if err != nil || levelID <= 0 {
		c.HTML(http.StatusBadRequest, "errors-400.html", gin.H{"Error": "Неверный ID уровня"})
		return
	}
	userID := c.GetUint("userID")

	var input CreateQuestionInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "questions-new.html", gin.H{
			"LevelID": levelID,
			"Error":   "Неверные данные: " + err.Error(),
			"csrf":    csrf.GetToken(c),
		})
		return
	}

	question := &Question{
		Text: sanitize.StripHTML(input.Text),
		Hint: sanitize.StripHTML(input.Hint),
	}

	if err := h.questionService.Create(c.Request.Context(), uint(levelID), question, userID); err != nil {
		log.Error().Err(err).Int("level_id", levelID).Uint("user", userID).Msg("CreateQuestion: failed to create question")
		render.Page(c, http.StatusInternalServerError, "questions-new.html", gin.H{
			"LevelID": levelID,
			"Error":   err.Error(),
			"csrf":    csrf.GetToken(c),
		})
		return
	}

	gameID, _ := strconv.Atoi(c.Param("id"))
	c.Redirect(http.StatusFound, "/games/"+strconv.Itoa(gameID)+"/levels/"+c.Param("level_id")+"/questions")
}

// EditQuestionForm отображает форму редактирования вопроса.
func (h *LevelHandler) EditQuestionForm(c *gin.Context) {
	questionID, err := strconv.Atoi(c.Param("question_id"))
	if err != nil || questionID <= 0 {
		c.HTML(http.StatusBadRequest, "errors-400.html", gin.H{"Error": "Неверный ID вопроса"})
		return
	}
	userID := c.GetUint("userID")

	question, err := h.questionService.GetByID(c.Request.Context(), uint(questionID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.HTML(http.StatusNotFound, "errors-404.html", nil)
		} else {
			log.Error().Err(err).Int("question_id", questionID).Msg("EditQuestionForm: failed to get question")
			c.HTML(http.StatusInternalServerError, "errors-500.html", nil)
		}
		return
	}

	level, err := h.levelService.GetByID(c.Request.Context(), question.LevelID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.HTML(http.StatusNotFound, "errors-404.html", nil)
		} else {
			log.Error().Err(err).Uint("level_id", question.LevelID).Msg("EditQuestionForm: failed to get level")
			c.HTML(http.StatusInternalServerError, "errors-500.html", nil)
		}
		return
	}

	ok, err := h.authorizer.IsUserManager(level.GameID, userID)
	if err != nil {
		log.Error().Err(err).Uint("game_id", level.GameID).Uint("user", userID).Msg("EditQuestionForm: failed to check manager")
		c.HTML(http.StatusInternalServerError, "errors-500.html", nil)
		return
	}
	if !ok {
		c.HTML(http.StatusForbidden, "errors-403.html", nil)
		return
	}

	render.Page(c, http.StatusOK, "questions-edit.html", gin.H{
		"Question": question,
		"csrf":     csrf.GetToken(c),
	})
}

// UpdateQuestion обновляет вопрос.
func (h *LevelHandler) UpdateQuestion(c *gin.Context) {
	questionID, err := strconv.Atoi(c.Param("question_id"))
	if err != nil || questionID <= 0 {
		c.HTML(http.StatusBadRequest, "errors-400.html", gin.H{"Error": "Неверный ID вопроса"})
		return
	}
	userID := c.GetUint("userID")

	var input UpdateQuestionInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "questions-edit.html", gin.H{
			"Error": "Неверные данные: " + err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	updated := &Question{
		Text: sanitize.StripHTML(input.Text),
		Hint: sanitize.StripHTML(input.Hint),
	}

	if err := h.questionService.Update(c.Request.Context(), uint(questionID), updated, userID); err != nil {
		log.Error().Err(err).Int("question_id", questionID).Uint("user", userID).Msg("UpdateQuestion: failed to update question")
		render.Page(c, http.StatusInternalServerError, "questions-edit.html", gin.H{
			"Question": updated,
			"Error":    err.Error(),
			"csrf":     csrf.GetToken(c),
		})
		return
	}

	gameID, _ := strconv.Atoi(c.Param("id"))
	c.Redirect(http.StatusFound, "/games/"+strconv.Itoa(gameID)+"/levels/"+c.Param("level_id")+"/questions")
}

// DeleteQuestion удаляет вопрос.
func (h *LevelHandler) DeleteQuestion(c *gin.Context) {
	questionID, err := strconv.Atoi(c.Param("question_id"))
	if err != nil || questionID <= 0 {
		c.HTML(http.StatusBadRequest, "errors-400.html", gin.H{"Error": "Неверный ID вопроса"})
		return
	}
	userID := c.GetUint("userID")

	if err := h.questionService.Delete(c.Request.Context(), uint(questionID), userID); err != nil {
		log.Error().Err(err).Int("question_id", questionID).Uint("user", userID).Msg("DeleteQuestion: failed to delete question")
		c.HTML(http.StatusForbidden, "errors-403.html", gin.H{"Error": err.Error()})
		return
	}

	gameID, _ := strconv.Atoi(c.Param("id"))
	c.Redirect(http.StatusFound, "/games/"+strconv.Itoa(gameID)+"/levels/"+c.Param("level_id")+"/questions")
}

// ----- Ответы -----

// ListAnswers отображает список ответов вопроса.
func (h *LevelHandler) ListAnswers(c *gin.Context) {
	questionID, err := strconv.Atoi(c.Param("question_id"))
	if err != nil || questionID <= 0 {
		c.HTML(http.StatusBadRequest, "errors-400.html", gin.H{"Error": "Неверный ID вопроса"})
		return
	}
	userID := c.GetUint("userID")

	question, err := h.questionService.GetByID(c.Request.Context(), uint(questionID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.HTML(http.StatusNotFound, "errors-404.html", nil)
		} else {
			log.Error().Err(err).Int("question_id", questionID).Msg("ListAnswers: failed to get question")
			c.HTML(http.StatusInternalServerError, "errors-500.html", nil)
		}
		return
	}

	level, err := h.levelService.GetByID(c.Request.Context(), question.LevelID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.HTML(http.StatusNotFound, "errors-404.html", nil)
		} else {
			log.Error().Err(err).Uint("level_id", question.LevelID).Msg("ListAnswers: failed to get level")
			c.HTML(http.StatusInternalServerError, "errors-500.html", nil)
		}
		return
	}

	ok, err := h.authorizer.IsUserManager(level.GameID, userID)
	if err != nil {
		log.Error().Err(err).Uint("game_id", level.GameID).Uint("user", userID).Msg("ListAnswers: failed to check manager")
		c.HTML(http.StatusInternalServerError, "errors-500.html", nil)
		return
	}
	if !ok {
		c.HTML(http.StatusForbidden, "errors-403.html", nil)
		return
	}

	answers, err := h.answerService.ListByQuestion(c.Request.Context(), uint(questionID))
	if err != nil {
		log.Error().Err(err).Int("question_id", questionID).Msg("ListAnswers: failed to list answers")
		c.HTML(http.StatusInternalServerError, "errors-500.html", nil)
		return
	}

	render.Page(c, http.StatusOK, "answers-list.html", gin.H{
		"QuestionID": questionID,
		"Answers":    answers,
		"csrf":       csrf.GetToken(c),
	})
}

// CreateAnswer создаёт новый ответ.
func (h *LevelHandler) CreateAnswer(c *gin.Context) {
	questionID, err := strconv.Atoi(c.Param("question_id"))
	if err != nil || questionID <= 0 {
		c.HTML(http.StatusBadRequest, "errors-400.html", gin.H{"Error": "Неверный ID вопроса"})
		return
	}
	userID := c.GetUint("userID")

	var input CreateAnswerInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "answers-list.html", gin.H{
			"QuestionID": questionID,
			"Error":      "Неверные данные: " + err.Error(),
			"csrf":       csrf.GetToken(c),
		})
		return
	}

	answer := &Answer{
		Code: sanitize.StripHTML(input.Code),
	}

	if err := h.answerService.Create(c.Request.Context(), uint(questionID), answer, userID); err != nil {
		log.Error().Err(err).Int("question_id", questionID).Uint("user", userID).Msg("CreateAnswer: failed to create answer")
		render.Page(c, http.StatusInternalServerError, "answers-list.html", gin.H{
			"QuestionID": questionID,
			"Error":      err.Error(),
			"csrf":       csrf.GetToken(c),
		})
		return
	}

	gameID, _ := strconv.Atoi(c.Param("id"))
	c.Redirect(http.StatusFound, "/games/"+strconv.Itoa(gameID)+"/levels/"+c.Param("level_id")+"/questions/"+c.Param("question_id")+"/answers")
}

// DeleteAnswer удаляет ответ.
func (h *LevelHandler) DeleteAnswer(c *gin.Context) {
	answerID, err := strconv.Atoi(c.Param("answer_id"))
	if err != nil || answerID <= 0 {
		c.HTML(http.StatusBadRequest, "errors-400.html", gin.H{"Error": "Неверный ID ответа"})
		return
	}
	userID := c.GetUint("userID")

	if err := h.answerService.Delete(c.Request.Context(), uint(answerID), userID); err != nil {
		log.Error().Err(err).Int("answer_id", answerID).Uint("user", userID).Msg("DeleteAnswer: failed to delete answer")
		c.HTML(http.StatusForbidden, "errors-403.html", gin.H{"Error": err.Error()})
		return
	}

	gameID, _ := strconv.Atoi(c.Param("id"))
	c.Redirect(http.StatusFound, "/games/"+strconv.Itoa(gameID)+"/levels/"+c.Param("level_id")+"/questions/"+c.Param("question_id")+"/answers")
}
