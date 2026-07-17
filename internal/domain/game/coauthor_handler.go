// internal/domain/game/coauthor_handler.go
package game

import (
	"net/http"
	"strconv"

	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/validation"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	csrf "github.com/utrack/gin-csrf"
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
func (h *CoAuthorHandler) ManageCoAuthors(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID := c.GetUint("userID")

	coAuthors, err := h.coAuthorSvc.List(uint(gameID))
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

// AddCoAuthor добавляет соавтора.
func (h *CoAuthorHandler) AddCoAuthor(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	ownerID := c.GetUint("userID")

	if err := limitRequestBody(c, 1*1024*1024); err != nil {
		render.RenderError(c, http.StatusBadRequest, err.Error())
		return
	}

	var input AddCoAuthorInput
	if err := c.ShouldBind(&input); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверные данные: "+err.Error())
		return
	}
	if err := validation.ValidatePositiveUint("ID пользователя", input.UserID); err != nil {
		render.RenderError(c, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.coAuthorSvc.Add(uint(gameID), input.UserID, ownerID); err != nil {
		render.RenderError(c, http.StatusForbidden, err.Error())
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/co-authors")
}

// RemoveCoAuthor удаляет соавтора.
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
