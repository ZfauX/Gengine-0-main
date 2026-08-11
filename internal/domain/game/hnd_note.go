// internal/domain/game/note_handler.go
package game

import (
	"errors"
	"net/http"
	"strconv"

	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/sanitize"
	"gengine-0/internal/pkg/validation"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// NoteHandler обрабатывает запросы, связанные с заметками к играм.
type NoteHandler struct {
	noteService *NoteService
}

// NewNoteHandler создаёт новый NoteHandler.
func NewNoteHandler(noteService *NoteService) *NoteHandler {
	return &NoteHandler{noteService: noteService}
}

// renderNoteError (DEEP-REVIEW PASS-2 #10): ErrNoteForbidden → 403;
// ErrNoteInvalidLevel → 400 (ошибка клиента); прочие ошибки (БД и т.п.) → 500
// с общим текстом. Раньше ВСЕ ошибки были 403 с сырым err.Error() —
// внутренние детали утекали клиенту.
func renderNoteError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrNoteForbidden):
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": render.Tr(c, "handler.forbidden"), "code": "forbidden"})
		return
	case errors.Is(err, ErrNoteInvalidLevel):
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Уровень не принадлежит этой игре", "code": "invalid_level"})
		return
	default:
		log.Error().Err(err).Msg("Note handler error")
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": render.Tr(c, "handler.internal_error"), "code": "internal_error"})
	}
}

// Notes возвращает заметки к игре в формате JSON.
// @Summary Список заметок
// @Tags notes
// @Produce json
// @Param id path int true "ID игры"
// @Success 200 {object} map[string]interface{} "Список заметок"
// @Failure 400 {object} map[string]interface{} "Неверный ID"
// @Failure 403 {object} map[string]interface{} "handler.forbidden"
// @Router /games/{id}/notes [get]
// @Security JWT
func (h *NoteHandler) Notes(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": render.Tr(c, "handler.invalid_game_id"),
			"code":  "bad_request",
		})
		return
	}
	userID := c.GetUint("userID")
	notes, err := h.noteService.ListByGame(c.Request.Context(), uint(gameID), userID)
	if err != nil {
		renderNoteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"notes": notes})
}

// CreateNote создаёт новую заметку.
// @Summary Создание заметки
// @Tags notes
// @Accept json
// @Produce json
// @Param id path int true "ID игры"
// @Param body body object true "Данные заметки"
// @Success 201 {object} map[string]interface{} "Заметка создана"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Failure 403 {object} map[string]interface{} "handler.forbidden"
// @Router /games/{id}/notes [post]
// @Security JWT
func (h *NoteHandler) CreateNote(c *gin.Context) {
	gameID, parseErr := strconv.Atoi(c.Param("id"))
	if parseErr != nil || gameID <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": render.Tr(c, "handler.invalid_game_id"),
			"code":  "bad_request",
		})
		return
	}
	userID := c.GetUint("userID")

	if limitErr := LimitRequestBody(c, 1*1024*1024); limitErr != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": limitErr.Error(),
			"code":  "bad_request",
		})
		return
	}

	var input struct {
		LevelID *uint  `json:"level_id"`
		Text    string `json:"text" binding:"required"`
	}
	if bindErr := c.ShouldBindJSON(&input); bindErr != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Неверный формат данных: " + bindErr.Error(),
			"code":  "validation_error",
		})
		return
	}
	if validateErr := validation.ValidateString("Текст заметки", input.Text, 1, 1000); validateErr != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": validateErr.Error(),
			"code":  "validation_error",
		})
		return
	}
	input.Text = sanitize.StripHTML(input.Text)

	note, createErr := h.noteService.Create(c.Request.Context(), uint(gameID), input.LevelID, userID, input.Text)
	if createErr != nil {
		renderNoteError(c, createErr)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"note": note})
}

// DeleteNote удаляет заметку.
// @Summary Удаление заметки
// @Tags notes
// @Produce json
// @Param note_id path int true "ID заметки"
// @Success 200 {object} map[string]interface{} "Заметка удалена"
// @Failure 400 {object} map[string]interface{} "Неверный ID"
// @Failure 403 {object} map[string]interface{} "handler.forbidden"
// @Router /games/notes/{note_id} [delete]
// @Security JWT
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
		renderNoteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
