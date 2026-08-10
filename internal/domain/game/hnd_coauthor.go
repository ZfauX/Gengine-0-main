// internal/domain/game/coauthor_handler.go
package game

import (
	"errors"
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
// @Failure 403 {object} map[string]interface{} render.Tr(c, "handler.forbidden")
// @Router /games/{id}/co-authors [get]
// @Security JWT
func (h *CoAuthorHandler) ManageCoAuthors(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_game_id"))
		return
	}
	userID := c.GetUint("userID")
	isAdmin := middleware.IsAdmin(c)

	// Страница соавторов приватна: список соавторов виден только менеджерам
	// игры и админам (S6 — IDOR). Add/Remove уже проверяют права в сервисе.
	if !isAdmin {
		isManager, mgrErr := h.coAuthorSvc.IsUserManager(c.Request.Context(), uint(gameID), userID)
		if mgrErr != nil {
			log.Error().Err(mgrErr).Int("game_id", gameID).Msg("CoAuthorHandler.ManageCoAuthors: failed to check manager")
			render.RenderErrorPage(c, http.StatusInternalServerError)
			return
		}
		if !isManager {
			render.RenderErrorPage(c, http.StatusForbidden)
			return
		}
	}

	coAuthors, err := h.coAuthorSvc.List(c.Request.Context(), uint(gameID))
	if err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("CoAuthorHandler.ManageCoAuthors: failed to list coauthors")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	render.Page(c, http.StatusOK, "co_authors-manage.html", gin.H{
		"Title":         render.Tr(c, "nav.co_authors"),
		"GameID":        gameID,
		"CoAuthors":     coAuthors,
		"csrf":          csrf.GetToken(c),
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
		"Breadcrumbs": []map[string]string{
			{"name": "nav.home", "url": "/"},
			{"name": "nav.games", "url": "/games"},
			{"name": "game.breadcrumb_label", "url": "/games/" + c.Param("id")},
			{"name": "nav.co_authors"},
		},
	})
}

// renderCoAuthorManagePage рендерит страницу управления соавторами с заданными данными.
func (h *CoAuthorHandler) renderCoAuthorManagePage(c *gin.Context, gameID int, errs validation.FieldErrors) {
	coAuthors, listErr := h.coAuthorSvc.List(c.Request.Context(), uint(gameID))
	if listErr != nil {
		log.Error().Err(listErr).Int("game_id", gameID).Msg("AddCoAuthor: failed to list coauthors")
	}
	data := gin.H{
		"Title":         render.Tr(c, "nav.co_authors"),
		"GameID":        gameID,
		"CoAuthors":     coAuthors,
		"Error":         errs.Error(),
		"csrf":          csrf.GetToken(c),
		"CurrentUserID": c.GetUint("userID"),
		"IsAdmin":       middleware.IsAdmin(c),
		"Breadcrumbs": []map[string]string{
			{"name": "nav.home", "url": "/"},
			{"name": "nav.games", "url": "/games"},
			{"name": "game.breadcrumb_label", "url": "/games/" + strconv.Itoa(gameID)},
			{"name": "nav.co_authors"},
		},
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
// @Failure 403 {object} map[string]interface{} render.Tr(c, "handler.forbidden")
// @Router /games/{id}/co-authors [post]
// @Security JWT
func (h *CoAuthorHandler) AddCoAuthor(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_game_id"))
		return
	}
	ownerID := c.GetUint("userID")

	if err := LimitRequestBody(c, 1*1024*1024); err != nil {
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

	if err := h.coAuthorSvc.Add(c.Request.Context(), uint(gameID), input.UserID, ownerID, input.Role, input.Permissions); err != nil {
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
// @Failure 403 {object} map[string]interface{} render.Tr(c, "handler.forbidden")
// @Router /games/{id}/co-authors/{user_id}/delete [post]
// @Security JWT
func (h *CoAuthorHandler) RemoveCoAuthor(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_game_id"))
		return
	}
	userID, err := strconv.Atoi(c.Param("user_id"))
	if err != nil || userID <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_user_id"))
		return
	}
	ownerID := c.GetUint("userID")

	if err := h.coAuthorSvc.Remove(c.Request.Context(), uint(gameID), uint(userID), ownerID); err != nil {
		// S-2 (pass 33): «только владелец» → 403; реальная DB-ошибка → 500,
		// а не сырая строка в 403.
		if errors.Is(err, ErrNotOwner) {
			render.RenderError(c, http.StatusForbidden, render.LocalizeError(c, err.Error()))
		} else {
			log.Error().Err(err).Int("game_id", gameID).Int("user_id", userID).Msg("RemoveCoAuthor: failed to remove co-author")
			render.RenderErrorPage(c, http.StatusInternalServerError)
		}
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/co-authors")
}
