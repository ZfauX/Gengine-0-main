// internal/domain/admin/handler.go
package admin

import (
	"errors"
	"net/http"
	"strconv"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/audit"
	"gengine-0/internal/pkg/render"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	csrf "github.com/utrack/gin-csrf"
	"gorm.io/gorm"
)

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

// ListUsers отображает список всех пользователей с возможностью фильтрации по роли.
func (h *AdminHandler) ListUsers(c *gin.Context) {
	ctx := c.Request.Context()
	role := c.Query("role")
	users, err := h.userRepo.List(ctx, role)
	if err != nil {
		log.Error().Err(err).Str("role", role).Msg("ListUsers failed")
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}

	render.Page(c, http.StatusOK, "admin-users.html", gin.H{
		"Users":         users,
		"Role":          role,
		"CurrentUserID": c.GetUint("userID"),
		"IsAdmin":       true,
		"csrf":          csrf.GetToken(c),
	})
}

// ToggleAdmin переключает роль пользователя между user и admin.
func (h *AdminHandler) ToggleAdmin(c *gin.Context) {
	idStr := c.Param("id")
	userID, err := strconv.Atoi(idStr)
	if err != nil || userID <= 0 {
		log.Warn().Str("id", idStr).Msg("ToggleAdmin: invalid user ID")
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}

	ctx := c.Request.Context()
	u, err := h.userRepo.GetByID(ctx, uint(userID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Warn().Int("user_id", userID).Msg("ToggleAdmin: user not found")
		} else {
			log.Error().Err(err).Int("user_id", userID).Msg("ToggleAdmin: failed to get user")
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

	c.Redirect(http.StatusFound, "/admin/users")
}

// DeleteUser удаляет пользователя.
func (h *AdminHandler) DeleteUser(c *gin.Context) {
	idStr := c.Param("id")
	userID, err := strconv.Atoi(idStr)
	if err != nil || userID <= 0 {
		log.Warn().Str("id", idStr).Msg("DeleteUser: invalid user ID")
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}

	if err := h.userRepo.Delete(c.Request.Context(), uint(userID)); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Warn().Int("user_id", userID).Msg("DeleteUser: user not found")
		} else {
			log.Error().Err(err).Int("user_id", userID).Msg("DeleteUser: failed to delete user")
		}
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}

	c.Redirect(http.StatusFound, "/admin/users")
}

// ---------- Игры ----------

// ListGames отображает все игры (включая черновики) с фильтрацией.
func (h *AdminHandler) ListGames(c *gin.Context) {
	ctx := c.Request.Context()
	status := c.Query("status")
	query := h.gameRepo.Model(ctx).Preload("Author")
	switch status {
	case "draft":
		query = query.Where("is_draft = true")
	case "published":
		query = query.Where("is_draft = false")
	}
	games, err := h.gameRepo.ListFiltered(ctx, query, 0, 1000)
	if err != nil {
		log.Error().Err(err).Str("status", status).Msg("ListGames failed")
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}

	render.Page(c, http.StatusOK, "admin-games.html", gin.H{
		"Games":         games,
		"Status":        status,
		"CurrentUserID": c.GetUint("userID"),
		"IsAdmin":       true,
		"csrf":          csrf.GetToken(c),
	})
}

// DeleteGame удаляет любую игру (административное действие).
func (h *AdminHandler) DeleteGame(c *gin.Context) {
	idStr := c.Param("id")
	gameID, err := strconv.Atoi(idStr)
	if err != nil || gameID <= 0 {
		log.Warn().Str("id", idStr).Msg("DeleteGame: invalid game ID")
		c.Redirect(http.StatusFound, "/admin/games")
		return
	}

	if err := h.gameRepo.Delete(c.Request.Context(), uint(gameID)); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Warn().Int("game_id", gameID).Msg("DeleteGame: game not found")
		} else {
			log.Error().Err(err).Int("game_id", gameID).Msg("DeleteGame: failed to delete game")
		}
		c.Redirect(http.StatusFound, "/admin/games")
		return
	}

	c.Redirect(http.StatusFound, "/admin/games")
}

// ---------- Аудит ----------

// AuditLog отображает страницу с записями аудита (с пагинацией и фильтрацией).
func (h *AdminHandler) AuditLog(c *gin.Context) {
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil || page < 1 {
		page = 1
	}
	perPage, err := strconv.Atoi(c.DefaultQuery("per_page", "20"))
	if err != nil || perPage < 1 || perPage > 100 {
		perPage = 20
	}

	userIDStr := c.Query("user_id")
	action := c.Query("action")

	logs, total, err := h.auditService.List(c.Request.Context(), userIDStr, action, page, perPage)
	if err != nil {
		log.Error().Err(err).Str("user_id", userIDStr).Str("action", action).Msg("AuditLog list failed")
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}

	totalPages := int((total + int64(perPage) - 1) / int64(perPage))
	if totalPages < 1 {
		totalPages = 1
	}
	prevPage := page - 1
	if prevPage < 1 {
		prevPage = 1
	}
	nextPage := page + 1
	if nextPage > totalPages {
		nextPage = totalPages
	}

	render.Page(c, http.StatusOK, "admin-audit.html", gin.H{
		"Logs":          logs,
		"Page":          page,
		"TotalPages":    totalPages,
		"PrevPage":      prevPage,
		"NextPage":      nextPage,
		"UserID":        userIDStr,
		"Action":        action,
		"CurrentUserID": c.GetUint("userID"),
		"IsAdmin":       true,
		"csrf":          csrf.GetToken(c),
	})
}

// ---------- Бекапы ----------

// ListBackups отображает страницу с историей резервных копий.
func (h *AdminHandler) ListBackups(c *gin.Context) {
	backups, err := h.backupService.List(c.Request.Context())
	if err != nil {
		log.Error().Err(err).Msg("ListBackups failed")
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
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

// CreateBackup запускает ручное создание бекапа.
func (h *AdminHandler) CreateBackup(c *gin.Context) {
	if err := h.backupService.CreateNow(c.Request.Context()); err != nil {
		log.Error().Err(err).Msg("CreateBackup failed")
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}
	c.Redirect(http.StatusFound, "/admin/backups")
}

// DownloadBackup отдаёт файл бекапа по ID.
func (h *AdminHandler) DownloadBackup(c *gin.Context) {
	idStr := c.Param("id")
	backupID, err := strconv.Atoi(idStr)
	if err != nil || backupID <= 0 {
		log.Warn().Str("id", idStr).Msg("DownloadBackup: invalid backup ID")
		c.HTML(http.StatusBadRequest, "errors/400.html", nil)
		return
	}

	path, err := h.backupService.Download(c.Request.Context(), uint(backupID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Warn().Int("backup_id", backupID).Msg("DownloadBackup: backup not found")
			c.HTML(http.StatusNotFound, "errors/404.html", nil)
		} else {
			log.Error().Err(err).Int("backup_id", backupID).Msg("DownloadBackup: failed to download backup")
			c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		}
		return
	}
	c.File(path)
}

// RotateBackups запускает принудительную ротацию старых бекапов.
func (h *AdminHandler) RotateBackups(c *gin.Context) {
	if err := h.backupService.RotateBackups(c.Request.Context()); err != nil {
		log.Error().Err(err).Msg("RotateBackups failed")
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}
	c.Redirect(http.StatusFound, "/admin/backups")
}
