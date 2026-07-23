// internal/domain/admin/handler.go
package admin

import (
	"errors"
	"net/http"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/audit"
	"gengine-0/internal/pkg/render"

	csrf "gengine-0/internal/pkg/csrf"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// ---------- Входные структуры для валидации ----------

// IDRequest используется для валидации ID в URL.
type IDRequest struct {
	ID uint `uri:"id" binding:"required,gt=0"`
}

// ListUsersRequest используется для фильтрации и пагинации списка пользователей.
type ListUsersRequest struct {
	Role    string `form:"role" binding:"omitempty,oneof=user admin"`
	Page    int    `form:"page" binding:"omitempty,min=1"`
	PerPage int    `form:"per_page" binding:"omitempty,min=1,max=100"`
}

// ListGamesRequest используется для фильтрации и пагинации списка игр.
type ListGamesRequest struct {
	Status  string `form:"status" binding:"omitempty,oneof=draft published"`
	Page    int    `form:"page" binding:"omitempty,min=1"`
	PerPage int    `form:"per_page" binding:"omitempty,min=1,max=100"`
}

// AuditLogRequest используется для фильтрации и пагинации аудита.
type AuditLogRequest struct {
	Page    int    `form:"page" binding:"omitempty,min=1"`
	PerPage int    `form:"per_page" binding:"omitempty,min=1,max=100"`
	UserID  string `form:"user_id"`
	Action  string `form:"action"`
}

// AdminHandler управляет административной панелью.
type AdminHandler struct {
	userRepo      user.UserRepository
	gameRepo      game.GameRepository
	backupService *BackupService
	auditService  *audit.Service
}

// NewAdminHandler создаёт новый AdminHandler.
func NewAdminHandler(
	userRepo user.UserRepository,
	gameRepo game.GameRepository,
	backupSvc *BackupService,
	auditSvc *audit.Service,
) *AdminHandler {
	return &AdminHandler{
		userRepo:      userRepo,
		gameRepo:      gameRepo,
		backupService: backupSvc,
		auditService:  auditSvc,
	}
}

// ---------- Пользователи ----------

// Dashboard отображает главную страницу админ-панели.
// @Summary Панель управления администратора
// @Description Отображает главную страницу админ-панели с общей статистикой (пользователи, игры, аудит, бэкапы)
// @Tags admin
// @Produce html
// @Success 200 {string} html "Страница админ-панели"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён (не администратор)"
// @Router /admin [get]
// @Security JWT
func (h *AdminHandler) Dashboard(c *gin.Context) {
	ctx := c.Request.Context()

	userCount, err := h.userRepo.Count(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Dashboard: failed to count users")
		userCount = 0
	}

	gameCount, err := h.gameRepo.Count(ctx, h.gameRepo.Model(ctx))
	if err != nil {
		log.Error().Err(err).Msg("Dashboard: failed to count games")
		gameCount = 0
	}

	auditCount, err := h.auditService.Count(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Dashboard: failed to count audit logs")
		auditCount = 0
	}

	backupCount, err := h.backupService.backupRepo.Count(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Dashboard: failed to count backups")
		backupCount = 0
	}

	render.Page(c, http.StatusOK, "admin-dashboard.html", gin.H{
		"UserCount":     userCount,
		"GameCount":     gameCount,
		"AuditCount":    auditCount,
		"BackupCount":   backupCount,
		"CurrentUserID": c.GetUint("userID"),
		"IsAdmin":       true,
		"csrf":          csrf.GetToken(c),
	})
}

// ListUsers отображает список пользователей.
// @Summary Список пользователей
// @Description Отображает список всех пользователей с фильтром по роли и пагинацией
// @Tags admin
// @Produce html
// @Param role query string false "Роль пользователя (user, admin)"
// @Param page query int false "Номер страницы" default(1)
// @Param per_page query int false "Количество записей на странице" default(20)
// @Success 200 {string} html "Страница со списком пользователей"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Router /admin/users [get]
// @Security JWT
func (h *AdminHandler) ListUsers(c *gin.Context) {
	var req ListUsersRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		req.Role = ""
		req.Page = 1
		req.PerPage = 20
	}
	if req.Page < 1 {
		req.Page = 1
	}
	if req.PerPage < 1 || req.PerPage > 100 {
		req.PerPage = 20
	}

	ctx := c.Request.Context()

	total, err := h.userRepo.CountByRole(ctx, req.Role)
	if err != nil {
		log.Error().Err(err).Str("role", req.Role).Msg("ListUsers: failed to count users")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	offset := (req.Page - 1) * req.PerPage
	users, err := h.userRepo.ListPaginated(ctx, req.Role, offset, req.PerPage)
	if err != nil {
		log.Error().Err(err).Str("role", req.Role).Msg("ListUsers: failed to list users")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	totalPages := int((total + int64(req.PerPage) - 1) / int64(req.PerPage))
	if totalPages < 1 {
		totalPages = 1
	}
	prevPage := req.Page - 1
	if prevPage < 1 {
		prevPage = 1
	}
	nextPage := req.Page + 1
	if nextPage > totalPages {
		nextPage = totalPages
	}

	render.Page(c, http.StatusOK, "admin-users.html", gin.H{
		"Users":         users,
		"Role":          req.Role,
		"Page":          req.Page,
		"PerPage":       req.PerPage,
		"TotalPages":    totalPages,
		"PrevPage":      prevPage,
		"NextPage":      nextPage,
		"Total":         total,
		"CurrentUserID": c.GetUint("userID"),
		"IsAdmin":       true,
		"csrf":          csrf.GetToken(c),
	})
}

// ToggleAdmin переключает роль пользователя между admin и user.
// @Summary Переключение роли пользователя
// @Description Делает пользователя администратором или обычным пользователем
// @Tags admin
// @Accept x-www-form-urlencoded
// @Produce html
// @Param id path int true "ID пользователя"
// @Success 302 {string} string "Перенаправление на /admin/users"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Router /admin/users/{id}/toggle-admin [post]
// @Security JWT
func (h *AdminHandler) ToggleAdmin(c *gin.Context) {
	var req IDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		log.Warn().Err(err).Msg("ToggleAdmin: invalid user ID")
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}

	ctx := c.Request.Context()
	u, err := h.userRepo.GetByID(ctx, req.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Warn().Uint("user_id", req.ID).Msg("ToggleAdmin: user not found")
		} else {
			log.Error().Err(err).Uint("user_id", req.ID).Msg("ToggleAdmin: failed to get user")
		}
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}

	if u.Role == "admin" {
		u.Role = "user"
	} else {
		u.Role = "admin"
	}
	if err := h.userRepo.Update(ctx, u.ID, map[string]any{"role": u.Role}); err != nil {
		log.Error().Err(err).Uint("user", u.ID).Msg("ToggleAdmin: failed to update role")
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}

	adminID := c.GetUint("userID")
	h.auditService.Log(adminID, "toggle_admin_role", "user", u.ID, "new_role: "+u.Role)
	c.Redirect(http.StatusFound, "/admin/users")
}

// DeleteUser удаляет пользователя.
// @Summary Удаление пользователя
// @Description Безвозвратно удаляет пользователя (административное действие)
// @Tags admin
// @Accept x-www-form-urlencoded
// @Produce html
// @Param id path int true "ID пользователя"
// @Success 302 {string} string "Перенаправление на /admin/users"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Router /admin/users/{id}/delete [post]
// @Security JWT
func (h *AdminHandler) DeleteUser(c *gin.Context) {
	var req IDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		log.Warn().Err(err).Msg("DeleteUser: invalid user ID")
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}

	if err := h.userRepo.Delete(c.Request.Context(), req.ID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Warn().Uint("user_id", req.ID).Msg("DeleteUser: user not found")
		} else {
			log.Error().Err(err).Uint("user_id", req.ID).Msg("DeleteUser: failed to delete user")
		}
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}

	adminID := c.GetUint("userID")
	h.auditService.Log(adminID, "delete_user", "user", req.ID, "")
	c.Redirect(http.StatusFound, "/admin/users")
}

// ---------- Игры ----------

// ListGames отображает список игр (административный).
// @Summary Список игр (административный)
// @Description Отображает все игры с фильтром по статусу (черновик / опубликована) и пагинацией
// @Tags admin
// @Produce html
// @Param status query string false "Статус игры (draft, published)"
// @Param page query int false "Номер страницы" default(1)
// @Param per_page query int false "Количество записей на странице" default(20)
// @Success 200 {string} html "Страница со списком игр"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Router /admin/games [get]
// @Security JWT
func (h *AdminHandler) ListGames(c *gin.Context) {
	var req ListGamesRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		req.Status = ""
		req.Page = 1
		req.PerPage = 20
	}
	if req.Page < 1 {
		req.Page = 1
	}
	if req.PerPage < 1 || req.PerPage > 100 {
		req.PerPage = 20
	}

	ctx := c.Request.Context()
	query := h.gameRepo.Model(ctx).Preload("Author")
	switch req.Status {
	case "draft":
		query = query.Where("is_draft = true")
	case "published":
		query = query.Where("is_draft = false")
	}

	total, err := h.gameRepo.Count(ctx, query)
	if err != nil {
		log.Error().Err(err).Str("status", req.Status).Msg("ListGames: failed to count games")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	offset := (req.Page - 1) * req.PerPage
	games, err := h.gameRepo.ListFiltered(ctx, query, offset, req.PerPage)
	if err != nil {
		log.Error().Err(err).Str("status", req.Status).Msg("ListGames: failed to list games")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	totalPages := int((total + int64(req.PerPage) - 1) / int64(req.PerPage))
	if totalPages < 1 {
		totalPages = 1
	}
	prevPage := req.Page - 1
	if prevPage < 1 {
		prevPage = 1
	}
	nextPage := req.Page + 1
	if nextPage > totalPages {
		nextPage = totalPages
	}

	render.Page(c, http.StatusOK, "admin-games.html", gin.H{
		"Games":         games,
		"Status":        req.Status,
		"Page":          req.Page,
		"PerPage":       req.PerPage,
		"TotalPages":    totalPages,
		"PrevPage":      prevPage,
		"NextPage":      nextPage,
		"Total":         total,
		"CurrentUserID": c.GetUint("userID"),
		"IsAdmin":       true,
		"csrf":          csrf.GetToken(c),
	})
}

// DeleteGame удаляет игру (административное действие).
// @Summary Удаление игры (административное)
// @Description Безвозвратно удаляет игру (доступно только администратору)
// @Tags admin
// @Accept x-www-form-urlencoded
// @Produce html
// @Param id path int true "ID игры"
// @Success 302 {string} string "Перенаправление на /admin/games"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Router /admin/games/{id}/delete [post]
// @Security JWT
func (h *AdminHandler) DeleteGame(c *gin.Context) {
	var req IDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		log.Warn().Err(err).Msg("DeleteGame: invalid game ID")
		c.Redirect(http.StatusFound, "/admin/games")
		return
	}

	if err := h.gameRepo.Delete(c.Request.Context(), req.ID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Warn().Uint("game_id", req.ID).Msg("DeleteGame: game not found")
		} else {
			log.Error().Err(err).Uint("game_id", req.ID).Msg("DeleteGame: failed to delete game")
		}
		c.Redirect(http.StatusFound, "/admin/games")
		return
	}

	adminID := c.GetUint("userID")
	h.auditService.Log(adminID, "delete_game", "game", req.ID, "")
	c.Redirect(http.StatusFound, "/admin/games")
}

// ---------- Аудит ----------

// AuditLog отображает журнал аудита.
// @Summary Журнал аудита
// @Description Отображает записи аудита с возможностью фильтрации по пользователю и действию, с пагинацией
// @Tags admin
// @Produce html
// @Param page query int false "Номер страницы" default(1)
// @Param per_page query int false "Количество записей на странице" default(20)
// @Param user_id query string false "ID пользователя"
// @Param action query string false "Действие (create, update, delete, login и т.д.)"
// @Success 200 {string} html "Страница аудита"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Router /admin/audit [get]
// @Security JWT
func (h *AdminHandler) AuditLog(c *gin.Context) {
	var req AuditLogRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		req.Page = 1
		req.PerPage = 20
	}
	if req.Page < 1 {
		req.Page = 1
	}
	if req.PerPage < 1 || req.PerPage > 100 {
		req.PerPage = 20
	}

	logs, total, err := h.auditService.List(c.Request.Context(), req.UserID, req.Action, req.Page, req.PerPage)
	if err != nil {
		log.Error().Err(err).Str("user_id", req.UserID).Str("action", req.Action).Msg("AuditLog list failed")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	totalPages := int((total + int64(req.PerPage) - 1) / int64(req.PerPage))
	if totalPages < 1 {
		totalPages = 1
	}
	prevPage := req.Page - 1
	if prevPage < 1 {
		prevPage = 1
	}
	nextPage := req.Page + 1
	if nextPage > totalPages {
		nextPage = totalPages
	}

	render.Page(c, http.StatusOK, "admin-audit.html", gin.H{
		"Logs":          logs,
		"Page":          req.Page,
		"TotalPages":    totalPages,
		"PrevPage":      prevPage,
		"NextPage":      nextPage,
		"UserID":        req.UserID,
		"Action":        req.Action,
		"CurrentUserID": c.GetUint("userID"),
		"IsAdmin":       true,
		"csrf":          csrf.GetToken(c),
	})
}

// ---------- Бекапы ----------

// ListBackups отображает список резервных копий.
// @Summary Список бекапов
// @Description Отображает список созданных резервных копий базы данных
// @Tags admin
// @Produce html
// @Success 200 {string} html "Страница бекапов"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Router /admin/backups [get]
// @Security JWT
func (h *AdminHandler) ListBackups(c *gin.Context) {
	backups, err := h.backupService.List(c.Request.Context())
	if err != nil {
		log.Error().Err(err).Msg("ListBackups failed")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	maxBackups := h.backupService.GetMaxBackups()
	render.Page(c, http.StatusOK, "admin-backups.html", gin.H{
		"Backups":       backups,
		"MaxBackups":    maxBackups,
		"Count":         len(backups),
		"CurrentUserID": c.GetUint("userID"),
		"IsAdmin":       true,
		"csrf":          csrf.GetToken(c),
	})
}

// CreateBackup создаёт новую резервную копию базы данных с помощью pg_dump.
// @Summary Создание бекапа
// @Description Создаёт новую резервную копию базы данных с помощью pg_dump
// @Tags admin
// @Accept x-www-form-urlencoded
// @Produce html
// @Success 302 {string} string "Перенаправление на /admin/backups"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Failure 500 {object} map[string]interface{} "Ошибка создания бекапа"
// @Router /admin/backups/create [post]
// @Security JWT
func (h *AdminHandler) CreateBackup(c *gin.Context) {
	if err := h.backupService.CreateNow(c.Request.Context()); err != nil {
		log.Error().Err(err).Msg("CreateBackup failed")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	c.Redirect(http.StatusFound, "/admin/backups")
}

// DownloadBackup отдаёт файл бекапа по ID.
func (h *AdminHandler) DownloadBackup(c *gin.Context) {
	var req IDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		log.Warn().Err(err).Msg("DownloadBackup: invalid backup ID")
		render.RenderError(c, http.StatusBadRequest, "")
		return
	}

	path, err := h.backupService.Download(c.Request.Context(), req.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Warn().Uint("backup_id", req.ID).Msg("DownloadBackup: backup not found")
			render.RenderErrorPage(c, http.StatusNotFound)
		} else {
			log.Error().Err(err).Uint("backup_id", req.ID).Msg("DownloadBackup: failed to download backup")
			render.RenderErrorPage(c, http.StatusInternalServerError)
		}
		return
	}
	c.File(path)
}

// RotateBackups запускает принудительную ротацию старых бекапов.
func (h *AdminHandler) RotateBackups(c *gin.Context) {
	if err := h.backupService.RotateBackups(c.Request.Context()); err != nil {
		log.Error().Err(err).Msg("RotateBackups failed")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	c.Redirect(http.StatusFound, "/admin/backups")
}
