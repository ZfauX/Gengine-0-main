// internal/domain/admin/handler.go
package admin

import (
	"net/http"
	"strconv"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/audit"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	csrf "github.com/utrack/gin-csrf"
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
// @Summary Панель управления администратора
// @Description Отображает главную страницу админ-панели с общей статистикой
// @Tags admin
// @Produce html
// @Success 200 {string} html "Страница админ-панели"
// @Router /admin [get]
// @Security JWT
func (h *AdminHandler) Dashboard(c *gin.Context) {
	ctx := c.Request.Context()
	userCount, _ := h.userRepo.Count(ctx)
	gameCount, _ := h.gameRepo.Count(ctx, h.gameRepo.Model(ctx))
	auditCount, _ := h.auditService.Count(ctx)
	backupCount, _ := h.backupService.backupRepo.Count(ctx)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock":  "admin-dashboard.html",
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
// @Summary Список пользователей
// @Description Отображает список всех пользователей с фильтром по роли
// @Tags admin
// @Produce html
// @Param role query string false "Роль пользователя (user, admin)"
// @Success 200 {string} html "Страница со списком пользователей"
// @Router /admin/users [get]
// @Security JWT
func (h *AdminHandler) ListUsers(c *gin.Context) {
	ctx := c.Request.Context()
	role := c.Query("role")
	users, err := h.userRepo.List(ctx, role)
	if err != nil {
		log.Error().Err(err).Msg("ListUsers failed")
		c.HTML(http.StatusInternalServerError, "layout.html", gin.H{"ContentBlock": "errors/500.html", "Error": err.Error()})
		return
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock":  "admin-users.html",
		"Users":         users,
		"Role":          role,
		"CurrentUserID": c.GetUint("userID"),
		"IsAdmin":       true,
		"csrf":          csrf.GetToken(c),
	})
}

// ToggleAdmin переключает роль пользователя между user и admin.
// @Summary Переключение роли пользователя
// @Description Делает пользователя администратором или обычным пользователем
// @Tags admin
// @Produce html
// @Param id path int true "ID пользователя"
// @Success 302 {string} string "Перенаправление на /admin/users"
// @Router /admin/users/{id}/toggle-admin [get]
// @Security JWT
func (h *AdminHandler) ToggleAdmin(c *gin.Context) {
	idStr := c.Param("id")
	userID, err := strconv.Atoi(idStr)
	if err != nil || userID <= 0 {
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}

	ctx := c.Request.Context()
	u, err := h.userRepo.GetByID(ctx, uint(userID))
	if err != nil {
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}

	if u.Role == "admin" {
		u.Role = "user"
	} else {
		u.Role = "admin"
	}
	if err := h.userRepo.Update(ctx, u.ID, map[string]any{"role": u.Role}); err != nil {
		log.Error().Err(err).Uint("user", u.ID).Msg("ToggleAdmin: failed to update")
	}

	c.Redirect(http.StatusFound, "/admin/users")
}

// DeleteUser удаляет пользователя.
// @Summary Удаление пользователя
// @Description Безвозвратно удаляет пользователя (административное действие)
// @Tags admin
// @Produce html
// @Param id path int true "ID пользователя"
// @Success 302 {string} string "Перенаправление на /admin/users"
// @Router /admin/users/{id}/delete [get]
// @Security JWT
func (h *AdminHandler) DeleteUser(c *gin.Context) {
	idStr := c.Param("id")
	userID, err := strconv.Atoi(idStr)
	if err != nil || userID <= 0 {
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}

	if err := h.userRepo.Delete(c.Request.Context(), uint(userID)); err != nil {
		log.Error().Err(err).Int("user", userID).Msg("DeleteUser: failed to delete")
	}
	c.Redirect(http.StatusFound, "/admin/users")
}

// ---------- Игры ----------

// ListGames отображает все игры (включая черновики) с фильтрацией.
// @Summary Список игр (административный)
// @Description Отображает все игры с фильтром по статусу (черновик / опубликована)
// @Tags admin
// @Produce html
// @Param status query string false "Статус игры (draft, published)"
// @Success 200 {string} html "Страница со списком игр"
// @Router /admin/games [get]
// @Security JWT
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
	games, err := h.gameRepo.ListFiltered(ctx, query, 0, 1000) // без пагинации
	if err != nil {
		log.Error().Err(err).Msg("ListGames failed")
		c.HTML(http.StatusInternalServerError, "layout.html", gin.H{"ContentBlock": "errors/500.html", "Error": err.Error()})
		return
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock":  "admin-games.html",
		"Games":         games,
		"Status":        status,
		"CurrentUserID": c.GetUint("userID"),
		"IsAdmin":       true,
		"csrf":          csrf.GetToken(c),
	})
}

// DeleteGame удаляет любую игру (административное действие).
// @Summary Удаление игры (административное)
// @Description Безвозвратно удаляет игру (доступно только администратору)
// @Tags admin
// @Produce html
// @Param id path int true "ID игры"
// @Success 302 {string} string "Перенаправление на /admin/games"
// @Router /admin/games/{id}/delete [get]
// @Security JWT
func (h *AdminHandler) DeleteGame(c *gin.Context) {
	idStr := c.Param("id")
	gameID, err := strconv.Atoi(idStr)
	if err != nil || gameID <= 0 {
		c.Redirect(http.StatusFound, "/admin/games")
		return
	}

	if err := h.gameRepo.Delete(c.Request.Context(), uint(gameID)); err != nil {
		log.Error().Err(err).Int("game", gameID).Msg("DeleteGame: failed to delete")
	}
	c.Redirect(http.StatusFound, "/admin/games")
}

// ---------- Аудит ----------

// AuditLog отображает страницу с записями аудита (с пагинацией и фильтрацией).
// @Summary Журнал аудита
// @Description Отображает записи аудита с возможностью фильтрации по пользователю и действию
// @Tags admin
// @Produce html
// @Param page query int false "Номер страницы" default(1)
// @Param per_page query int false "Количество записей на странице" default(20)
// @Param user_id query string false "ID пользователя"
// @Param action query string false "Действие (create, update, delete, login и т.д.)"
// @Success 200 {string} html "Страница аудита"
// @Router /admin/audit [get]
// @Security JWT
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

	logs, total, err := h.auditService.List(c.Request.Context(), userIDStr, action, page, perPage)
	if err != nil {
		log.Error().Err(err).Msg("AuditLog list failed")
		c.HTML(http.StatusInternalServerError, "layout.html", gin.H{"ContentBlock": "errors/500.html", "Error": err.Error()})
		return
	}

	totalPages := int((total + int64(perPage) - 1) / int64(perPage))
	prevPage := max(page-1, 1)
	nextPage := min(page+1, totalPages)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock":  "admin-audit.html",
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
// @Summary Список бекапов
// @Description Отображает список созданных резервных копий базы данных
// @Tags admin
// @Produce html
// @Success 200 {string} html "Страница бекапов"
// @Router /admin/backups [get]
// @Security JWT
func (h *AdminHandler) ListBackups(c *gin.Context) {
	backups, err := h.backupService.List(c.Request.Context())
	if err != nil {
		log.Error().Err(err).Msg("ListBackups failed")
		c.HTML(http.StatusInternalServerError, "layout.html", gin.H{"ContentBlock": "errors/500.html", "Error": err.Error()})
		return
	}
	maxBackups := h.backupService.GetMaxBackups()
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock":  "admin-backups.html",
		"Backups":       backups,
		"MaxBackups":    maxBackups,
		"Count":         len(backups),
		"CurrentUserID": c.GetUint("userID"),
		"IsAdmin":       true,
		"csrf":          csrf.GetToken(c),
	})
}

// CreateBackup запускает ручное создание бекапа.
// @Summary Создание бекапа
// @Description Создаёт новую резервную копию базы данных с помощью pg_dump
// @Tags admin
// @Produce html
// @Success 302 {string} string "Перенаправление на /admin/backups"
// @Router /admin/backups/create [post]
// @Security JWT
func (h *AdminHandler) CreateBackup(c *gin.Context) {
	if err := h.backupService.CreateNow(c.Request.Context()); err != nil {
		log.Error().Err(err).Msg("CreateBackup failed")
		c.HTML(http.StatusInternalServerError, "layout.html", gin.H{"ContentBlock": "errors/500.html", "Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/admin/backups")
}

// DownloadBackup отдаёт файл бекапа по ID.
// @Summary Скачать бекап
// @Description Скачивает файл резервной копии по ID
// @Tags admin
// @Produce application/octet-stream
// @Param id path int true "ID бекапа"
// @Success 200 {file} file "Файл бекапа"
// @Failure 404 {object} map[string]interface{} "Бекап не найден"
// @Router /admin/backups/{id}/download [get]
// @Security JWT
func (h *AdminHandler) DownloadBackup(c *gin.Context) {
	backupID, _ := strconv.Atoi(c.Param("id"))
	path, err := h.backupService.Download(c.Request.Context(), uint(backupID))
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}
	c.File(path)
}

// RotateBackups запускает принудительную ротацию старых бекапов.
// @Summary Ротация бекапов
// @Description Удаляет старые резервные копии, оставляя не более MaxBackups
// @Tags admin
// @Produce html
// @Success 302 {string} string "Перенаправление на /admin/backups"
// @Router /admin/backups/rotate [post]
// @Security JWT
func (h *AdminHandler) RotateBackups(c *gin.Context) {
	if err := h.backupService.RotateBackups(c.Request.Context()); err != nil {
		log.Error().Err(err).Msg("RotateBackups failed")
		c.HTML(http.StatusInternalServerError, "layout.html", gin.H{"ContentBlock": "errors/500.html", "Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/admin/backups")
}
