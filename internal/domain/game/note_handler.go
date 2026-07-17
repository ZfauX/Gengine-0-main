// internal/domain/game/note_handler.go
package game

import (
	"net/http"
	"strconv"

	apperr "gengine-0/internal/pkg/errors"
	"gengine-0/internal/pkg/sanitize"
	"gengine-0/internal/pkg/validation"

	"github.com/gin-gonic/gin"
)

// NoteHandler обрабатывает запросы, связанные с заметками к играм.
type NoteHandler struct {
	noteService *NoteService
}

// NewNoteHandler создаёт новый NoteHandler.
func NewNoteHandler(noteService *NoteService) *NoteHandler {
	return &NoteHandler{noteService: noteService}
}

// Notes возвращает заметки к игре в формате JSON.
func (h *NoteHandler) Notes(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Неверный ID игры",
			"code":  "bad_request",
		})
		return
	}
	userID := c.GetUint("userID")
	notes, err := h.noteService.ListByGame(c.Request.Context(), uint(gameID), userID)
	if err != nil {
		appErr := apperr.NewForbiddenError(err.Error())
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"notes": notes})
}

// CreateNote создаёт новую заметку.
func (h *NoteHandler) CreateNote(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Неверный ID игры",
			"code":  "bad_request",
		})
		return
	}
	userID := c.GetUint("userID")

	if err := limitRequestBody(c, 1*1024*1024); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
			"code":  "bad_request",
		})
		return
	}

	var input struct {
		LevelID *uint  `json:"level_id"`
		Text    string `json:"text" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Неверный формат данных: " + err.Error(),
			"code":  "validation_error",
		})
		return
	}
	if err := validation.ValidateString("Текст заметки", input.Text, 1, 1000); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
			"code":  "validation_error",
		})
		return
	}
	input.Text = sanitize.StripHTML(input.Text)

	note, err := h.noteService.Create(c.Request.Context(), uint(gameID), input.LevelID, userID, input.Text)
	if err != nil {
		appErr := apperr.NewForbiddenError(err.Error())
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"note": note})
}

// DeleteNote удаляет заметку.
func (h *NoteHandler) DeleteNote(c *gin.Context) {
	noteID, err := strconv.Atoi(c.Param("note_id"))
	if err != nil || noteID <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Неверный ID заметки",
			"code":  "bad_request",
		})
		return
	}
	userID := c.GetUint("userID")
	if err := h.noteService.Delete(c.Request.Context(), uint(noteID), userID); err != nil {
		appErr := apperr.NewForbiddenError(err.Error())
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
