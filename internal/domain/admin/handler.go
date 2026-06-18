// internal/domain/admin/handler.go
package admin

import (
	"net/http"
	"strconv"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/user"

	"github.com/utrack/gin-csrf"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// AdminHandler управляет административной панелью.
type AdminHandler struct {
	DB            *gorm.DB
	backupService *BackupService
	auditService  *AuditService
}

// NewAdminHandler создаёт новый AdminHandler.
func NewAdminHandler(db *gorm.DB, backupSvc *BackupService, auditSvc *AuditService) *AdminHandler {
	return &AdminHandler{
		DB:            db,
		backupService: backupSvc,
		auditService:  auditSvc,
	}
}

// ---------- Пользователи ----------

// ListUsers отображает список всех пользователей с возможностью фильтрации по роли.
func (h *AdminHandler) ListUsers(c *gin.Context) {
	role := c.Query("role")
	var users []user.User
	query := h.DB.Model(&user.User{})
	if role != "" {
		query = query.Where("role = ?", role)
	}
	query.Find(&users)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "admin-users.html",
		"Users":        users,
		"Role":         role,
		"csrf":         csrf.GetToken(c),
	})
}

// ToggleAdmin переключает роль пользователя между user и admin.
func (h *AdminHandler) ToggleAdmin(c *gin.Context) {
	idStr := c.Param("id")
	userID, err := strconv.Atoi(idStr)
	if err != nil || userID <= 0 {
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}

	var u user.User
	if err := h.DB.First(&u, userID).Error; err != nil {
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}

	if u.Role == "admin" {
		u.Role = "user"
	} else {
		u.Role = "admin"
	}
	h.DB.Save(&u)

	c.Redirect(http.StatusFound, "/admin/users")
}

// DeleteUser удаляет пользователя.
func (h *AdminHandler) DeleteUser(c *gin.Context) {
	idStr := c.Param("id")
	userID, err := strconv.Atoi(idStr)
	if err != nil || userID <= 0 {
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}

	h.DB.Delete(&user.User{}, userID)
	c.Redirect(http.StatusFound, "/admin/users")
}

// ---------- Игры ----------

// ListGames отображает все игры (включая черновики) с фильтрацией.
func (h *AdminHandler) ListGames(c *gin.Context) {
	status := c.Query("status")
	var games []game.Game
	query := h.DB.Preload("Author")
	switch status {
	case "draft":
		query = query.Where("is_draft = true")
	case "published":
		query = query.Where("is_draft = false")
	}
	query.Find(&games)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "admin-games.html",
		"Games":        games,
		"Status":       status,
		"csrf":         csrf.GetToken(c),
	})
}

// DeleteGame удаляет любую игру (административное действие).
func (h *AdminHandler) DeleteGame(c *gin.Context) {
	idStr := c.Param("id")
	gameID, err := strconv.Atoi(idStr)
	if err != nil || gameID <= 0 {
		c.Redirect(http.StatusFound, "/admin/games")
		return
	}

	h.DB.Delete(&game.Game{}, gameID)
	c.Redirect(http.StatusFound, "/admin/games")
}

// ---------- Аудит ----------

// AuditLog отображает страницу с записями аудита (с пагинацией и фильтрацией).
func (h *AdminHandler) AuditLog(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "20"))
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}

	userIDStr := c.Query("user_id")
	action := c.Query("action")

	logs, total, err := h.auditService.List(userIDStr, action, page, perPage)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}

	totalPages := int((total + int64(perPage) - 1) / int64(perPage))

	prevPage := page - 1
	if prevPage < 1 {
		prevPage = 1
	}
	nextPage := page + 1
	if nextPage > totalPages {
		nextPage = totalPages
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "admin-audit.html",
		"Logs":         logs,
		"Page":         page,
		"TotalPages":   totalPages,
		"PrevPage":     prevPage,
		"NextPage":     nextPage,
		"UserID":       userIDStr,
		"Action":       action,
		"csrf":         csrf.GetToken(c),
	})
}

// ---------- Бекапы ----------

// ListBackups отображает страницу с историей резервных копий.
func (h *AdminHandler) ListBackups(c *gin.Context) {
	backups, err := h.backupService.List()
	if err != nil {
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}
	maxBackups := h.backupService.GetMaxBackups()
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "admin-backups.html",
		"Backups":      backups,
		"MaxBackups":   maxBackups,
		"Count":        len(backups),
		"csrf":         csrf.GetToken(c),
	})
}

// CreateBackup запускает ручное создание бекапа.
func (h *AdminHandler) CreateBackup(c *gin.Context) {
	if err := h.backupService.CreateNow(); err != nil {
		c.HTML(http.StatusInternalServerError, "errors/500.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/admin/backups")
}

// DownloadBackup отдаёт файл бекапа по ID.
func (h *AdminHandler) DownloadBackup(c *gin.Context) {
	backupID, _ := strconv.Atoi(c.Param("id"))
	path, err := h.backupService.Download(uint(backupID))
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}
	c.File(path)
}

// RotateBackups запускает принудительную ротацию старых бекапов.
func (h *AdminHandler) RotateBackups(c *gin.Context) {
	if err := h.backupService.RotateBackups(); err != nil {
		c.HTML(http.StatusInternalServerError, "errors/500.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/admin/backups")
}