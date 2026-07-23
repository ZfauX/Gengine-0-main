// internal/domain/game/coauthor_handler.go
package game

import (
	"net/http"
	"strconv"

	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/validation"

	csrf "gengine-0/internal/pkg/csrf"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// CoAuthorHandler обрабатывает запросы, связанные с соавторами игр.
type CoAuthorHandler struct {
	coAuthorSvc CoAuthorServiceInterface
	auditSvc    AuditServiceInterface
}

// NewCoAuthorHandler создаёт новый CoAuthorHandler.
func NewCoAuthorHandler(
	coAuthorSvc CoAuthorServiceInterface,
	auditSvc AuditServiceInterface,
) *CoAuthorHandler {
	return &CoAuthorHandler{
		coAuthorSvc: coAuthorSvc,
		auditSvc:    auditSvc,
	}
}

// ManageCoAuthors отображает страницу управления соавторами.
// @Summary Управление соавторами
// @Tags coauthors
// @Produce html
// @Param id path int true "ID игры"
// @Success 200 {string} html "Страница соавторов"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Router /games/{id}/co-authors [get]
// @Security JWT
func (h *CoAuthorHandler) ManageCoAuthors(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID := c.GetUint("userID")

	coAuthors, err := h.coAuthorSvc.List(c.Request.Context(), uint(gameID))
	if err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("CoAuthorHandler.ManageCoAuthors: failed to list coauthors")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	isAdmin := middleware.IsAdmin(c)

	render.Page(c, http.StatusOK, "co_authors-manage.html", gin.H{
		"GameID":        gameID,
		"CoAuthors":     coAuthors,
		"csrf":          csrf.GetToken(c),
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
	})
}

// renderCoAuthorManagePage рендерит страницу управления соавторами с заданными данными.
func (h *CoAuthorHandler) renderCoAuthorManagePage(c *gin.Context, gameID int, errs validation.FieldErrors) {
	coAuthors, listErr := h.coAuthorSvc.List(c.Request.Context(), uint(gameID))
	if listErr != nil {
		log.Error().Err(listErr).Int("game_id", gameID).Msg("AddCoAuthor: failed to list coauthors")
	}
	data := gin.H{
		"GameID":        gameID,
		"CoAuthors":     coAuthors,
		"Error":         errs.Error(),
		"csrf":          csrf.GetToken(c),
		"CurrentUserID": c.GetUint("userID"),
		"IsAdmin":       middleware.IsAdmin(c),
	}
	if errs.HasErrors() {
		data["Errors"] = errs
	}
	render.Page(c, http.StatusBadRequest, "co_authors-manage.html", data)
}

// AddCoAuthor добавляет соавтора к игре.
// @Summary Добавление соавтора
// @Tags coauthors
// @Param id path int true "ID игры"
// @Param user_id formData int true "ID пользователя"
// @Success 302 {string} string "Перенаправление на /games/{id}/co-authors"
// @Failure 400 {object} map[string]interface{} "Ошибка"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Router /games/{id}/co-authors [post]
// @Security JWT
func (h *CoAuthorHandler) AddCoAuthor(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	ownerID := c.GetUint("userID")

	if err := limitRequestBody(c, 1*1024*1024); err != nil {
		errs := validation.FieldErrors{}
		errs.Add("form", err)
		h.renderCoAuthorManagePage(c, gameID, errs)
		return
	}

	var input AddCoAuthorInput
	if err := c.ShouldBind(&input); err != nil {
		errs := validation.FieldErrors{}
		errs.Add("user_id", err)
		h.renderCoAuthorManagePage(c, gameID, errs)
		return
	}
	if err := validation.ValidatePositiveUint("ID пользователя", input.UserID); err != nil {
		errs := validation.FieldErrors{}
		errs.Add("user_id", err)
		h.renderCoAuthorManagePage(c, gameID, errs)
		return
	}

	if err := h.coAuthorSvc.Add(uint(gameID), input.UserID, ownerID); err != nil {
		errs := validation.FieldErrors{}
		errs.Add("form", err)
		h.renderCoAuthorManagePage(c, gameID, errs)
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/co-authors")
}

// RemoveCoAuthor удаляет соавтора из игры.
// @Summary Удаление соавтора
// @Tags coauthors
// @Param id path int true "ID игры"
// @Param user_id path int true "ID пользователя"
// @Success 302 {string} string "Перенаправление на /games/{id}/co-authors"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Router /games/{id}/co-authors/{user_id}/delete [post]
// @Security JWT
func (h *CoAuthorHandler) RemoveCoAuthor(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID, err := strconv.Atoi(c.Param("user_id"))
	if err != nil || userID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID пользователя")
		return
	}
	ownerID := c.GetUint("userID")

	if err := h.coAuthorSvc.Remove(uint(gameID), uint(userID), ownerID); err != nil {
		render.RenderError(c, http.StatusForbidden, err.Error())
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/co-authors")
}
