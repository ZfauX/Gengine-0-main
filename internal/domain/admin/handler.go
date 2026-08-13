// internal/domain/admin/handler.go
package admin

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/audit"
	"gengine-0/internal/pkg/cache"
	"gengine-0/internal/pkg/crypto"
	"gengine-0/internal/pkg/i18n"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/validation"

	csrf "gengine-0/internal/pkg/csrf"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/bcrypt"
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
	Query   string `form:"query"`
	Page    int    `form:"page" binding:"omitempty,min=1"`
	PerPage int    `form:"per_page" binding:"omitempty,min=1,max=100"`
}

// ListGamesRequest используется для фильтрации и пагинации списка игр.
type ListGamesRequest struct {
	Status  string `form:"status" binding:"omitempty,oneof=draft published"`
	Query   string `form:"query"`
	Page    int    `form:"page" binding:"omitempty,min=1"`
	PerPage int    `form:"per_page" binding:"omitempty,min=1,max=100"`
}

// AuditLogRequest используется для фильтрации и пагинации аудита.
type AuditLogRequest struct {
	Page    int    `form:"page" binding:"omitempty,min=1"`
	PerPage int    `form:"per_page" binding:"omitempty,min=1,max=100"`
	UserID  string `form:"user_id"`
	Action  string `form:"action"`
	Query   string `form:"query"`
}

// AdminHandler управляет административной панелью.
type AdminHandler struct {
	userRepo         user.UserRepository
	gameRepo         game.GameRepository
	gameService      *game.GameService
	teamRepo         team.TeamRepository
	backupService    *BackupService
	auditService     *audit.Service
	refreshTokenRepo user.RefreshTokenRepository
	cacheStore       cache.CacheStore
}

// NewAdminHandler создаёт новый AdminHandler.
func NewAdminHandler(
	userRepo user.UserRepository,
	gameRepo game.GameRepository,
	gameService *game.GameService,
	teamRepo team.TeamRepository,
	backupSvc *BackupService,
	auditSvc *audit.Service,
	refreshTokenRepo user.RefreshTokenRepository,
	cacheStore cache.CacheStore,
) *AdminHandler {
	return &AdminHandler{
		userRepo:         userRepo,
		gameRepo:         gameRepo,
		gameService:      gameService,
		teamRepo:         teamRepo,
		backupService:    backupSvc,
		auditService:     auditSvc,
		refreshTokenRepo: refreshTokenRepo,
		cacheStore:       cacheStore,
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

	// PF-6 (pass 29): 5 COUNT были 5 round-trip на каждый заход; теперь один
	// запрос с подзапросами. Ошибка одного счётчика не роняет весь дашборд.
	var counts struct {
		UserCount   int64
		GameCount   int64
		TeamCount   int64
		AuditCount  int64
		BackupCount int64
	}
	err := h.gameRepo.RawScan(ctx, &counts, `
			SELECT
			(SELECT COUNT(*) FROM users WHERE deleted_at IS NULL) AS user_count,
			(SELECT COUNT(*) FROM games WHERE deleted_at IS NULL) AS game_count,
			(SELECT COUNT(*) FROM teams WHERE deleted_at IS NULL) AS team_count,
			(SELECT COUNT(*) FROM audit_logs WHERE deleted_at IS NULL) AS audit_count,
			(SELECT COUNT(*) FROM backups) AS backup_count
		`)
	if err != nil {
		log.Error().Err(err).Msg("Dashboard: failed to count dashboard stats")
	}

	render.Page(c, http.StatusOK, "admin-dashboard.html", gin.H{
		"Title":         "Админ-панель",
		"UserCount":     counts.UserCount,
		"GameCount":     counts.GameCount,
		"TeamCount":     counts.TeamCount,
		"AuditCount":    counts.AuditCount,
		"BackupCount":   counts.BackupCount,
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
// @Failure 403 {object} map[string]interface{} "handler.forbidden"
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
	// L-6 (pass 40): верхняя граница page - огромные OFFSET делают дорогие
	// запросы (слабая DoS-сторона для админов).
	if req.Page > 10000 {
		req.Page = 10000
	}
	if req.PerPage < 1 || req.PerPage > 100 {
		req.PerPage = 20
	}

	ctx := c.Request.Context()

	var err error
	var total int64
	var users []user.User
	offset := (req.Page - 1) * req.PerPage

	if req.Query != "" {
		total, err = h.userRepo.CountSearch(ctx, req.Query, req.Role)
		if err != nil {
			log.Error().Err(err).Str("query", req.Query).Msg("ListUsers: failed to count search")
			render.RenderErrorPage(c, http.StatusInternalServerError)
			return
		}
		users, err = h.userRepo.SearchPaginated(ctx, req.Query, req.Role, offset, req.PerPage)
		if err != nil {
			log.Error().Err(err).Str("query", req.Query).Msg("ListUsers: failed to search users")
			render.RenderErrorPage(c, http.StatusInternalServerError)
			return
		}
	} else {
		total, err = h.userRepo.CountByRole(ctx, req.Role)
		if err != nil {
			log.Error().Err(err).Str("role", req.Role).Msg("ListUsers: failed to count users")
			render.RenderErrorPage(c, http.StatusInternalServerError)
			return
		}
		users, err = h.userRepo.ListPaginated(ctx, req.Role, offset, req.PerPage)
		if err != nil {
			log.Error().Err(err).Str("role", req.Role).Msg("ListUsers: failed to list users")
			render.RenderErrorPage(c, http.StatusInternalServerError)
			return
		}
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

	errorFlash := render.GetFlash(c, "error")

	render.Page(c, http.StatusOK, "admin-users.html", gin.H{
		"Title":         "Пользователи",
		"Users":         users,
		"Role":          req.Role,
		"Query":         req.Query,
		"Page":          req.Page,
		"PerPage":       req.PerPage,
		"TotalPages":    totalPages,
		"PrevPage":      prevPage,
		"NextPage":      nextPage,
		"Total":         total,
		"CurrentUserID": c.GetUint("userID"),
		"IsAdmin":       true,
		"Error":         errorFlash,
		"csrf":          csrf.GetToken(c),
	})
}

// CreateUser — E-1 (pass 45): админ создаёт игрока.
// @Summary Создание пользователя
// @Tags admin
// @Accept x-www-form-urlencoded
// @Param email formData string true "Email"
// @Param name formData string true "Имя"
// @Param password formData string true "Пароль"
// @Success 302 {string} string "Редирект"
// @Router /admin/users/create [post]
// @Security JWT
func (h *AdminHandler) CreateUser(c *gin.Context) {
	email := strings.TrimSpace(c.PostForm("email"))
	name := strings.TrimSpace(c.PostForm("name"))
	password := c.PostForm("password")

	if email == "" || name == "" {
		render.SetFlash(c, "error", i18n.T("admin.create_user_invalid"))
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}
	// PASS-8 LOW #2: единая валидация пароля с регистрацией/сменой — раньше
	// только len>=8 (слабый пароль от админа).
	if err := validation.ValidatePasswordStrength(password); err != nil {
		render.SetFlash(c, "error", err.Error())
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}
	if _, err := h.userRepo.GetByEmail(c.Request.Context(), email); err == nil {
		render.SetFlash(c, "error", i18n.T("admin.create_user_exists"))
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(password), crypto.BcryptCost)
	if err != nil {
		log.Error().Err(err).Msg("CreateUser: failed to hash password")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	u := &user.User{Email: email, Name: name, Password: string(hashed), EmailVerified: true}
	if err := h.userRepo.Create(c.Request.Context(), u); err != nil {
		log.Error().Err(err).Str("email", email).Msg("CreateUser: failed to create")
		render.SetFlash(c, "error", i18n.T("admin.create_user_error"))
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}
	if h.auditService != nil {
		h.auditService.Log(c.GetUint("userID"), "admin.user_create", "users", u.ID, fmt.Sprintf("email=%s", email))
	}
	render.SetFlash(c, "success", i18n.T("admin.create_user_ok"))
	c.Redirect(http.StatusFound, "/admin/users")
}

// CreateTeam — E-2 (pass 45): админ создаёт команду (с капитаном).
// @Summary Создание команды
// @Tags admin
// @Accept x-www-form-urlencoded
// @Param name formData string true "Название"
// @Param captain_id formData uint true "ID капитана"
// @Success 302 {string} string "Редирект"
// @Router /admin/teams/create [post]
// @Security JWT
func (h *AdminHandler) CreateTeam(c *gin.Context) {
	name := strings.TrimSpace(c.PostForm("name"))
	captainID, _ := strconv.Atoi(c.PostForm("captain_id"))

	if name == "" || captainID <= 0 {
		render.SetFlash(c, "error", i18n.T("admin.create_team_invalid"))
		c.Redirect(http.StatusFound, "/admin/teams")
		return
	}
	if _, err := h.userRepo.GetByID(c.Request.Context(), uint(captainID)); err != nil {
		render.SetFlash(c, "error", i18n.T("admin.create_team_captain_missing"))
		c.Redirect(http.StatusFound, "/admin/teams")
		return
	}
	team := &team.Team{Name: name, CaptainID: uint(captainID)}
	if err := h.teamRepo.Create(c.Request.Context(), team); err != nil {
		log.Error().Err(err).Str("name", name).Msg("CreateTeam: failed to create")
		render.SetFlash(c, "error", i18n.T("admin.create_team_error"))
		c.Redirect(http.StatusFound, "/admin/teams")
		return
	}
	if h.auditService != nil {
		h.auditService.Log(c.GetUint("userID"), "admin.team_create", "teams", team.ID, fmt.Sprintf("name=%s", name))
	}
	render.SetFlash(c, "success", i18n.T("admin.create_team_ok"))
	c.Redirect(http.StatusFound, "/admin/teams")
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
// @Failure 403 {object} map[string]interface{} "handler.forbidden"
// @Router /admin/users/{id}/toggle-admin [post]
// @Security JWT
func (h *AdminHandler) ToggleAdmin(c *gin.Context) {
	var req IDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		log.Warn().Err(err).Msg("ToggleAdmin: invalid user ID")
		render.SetFlash(c, "error", i18n.T("generic.invalid_user_id"))
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}

	ctx := c.Request.Context()
	u, err := h.userRepo.GetByID(ctx, req.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Warn().Uint("user_id", req.ID).Msg("ToggleAdmin: user not found")
			render.SetFlash(c, "error", i18n.T("auth.user_not_found"))
		} else {
			log.Error().Err(err).Uint("user_id", req.ID).Msg("ToggleAdmin: failed to get user")
			render.SetFlash(c, "error", i18n.T("admin.user_get_error"))
		}
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}

	if u.Role == "admin" {
		// DEEP-REVIEW PASS-2 (#7): атомарный демоушен — нельзя разжаловать
		// ПОСЛЕДНЕГО админа (иначе /admin, /metrics, /swagger станут
		// недоступны). Раньше CountByRole + Update были раздельными → TOCTOU.
		demoted, demoteErr := h.userRepo.DemoteAdminIfNotLast(ctx, u.ID)
		if demoteErr != nil {
			log.Error().Err(demoteErr).Msg("ToggleAdmin: failed to demote admin")
			render.SetFlash(c, "error", i18n.T("admin.user_role_update_error"))
			c.Redirect(http.StatusFound, "/admin/users")
			return
		}
		if !demoted {
			render.SetFlash(c, "error", i18n.T("admin.last_admin_error"))
			c.Redirect(http.StatusFound, "/admin/users")
			return
		}
		u.Role = "user"
	} else {
		u.Role = "admin"
		if err := h.userRepo.Update(ctx, u.ID, map[string]any{"role": u.Role}); err != nil {
			log.Error().Err(err).Uint("user", u.ID).Msg("ToggleAdmin: failed to update role")
			render.SetFlash(c, "error", i18n.T("admin.user_role_update_error"))
			c.Redirect(http.StatusFound, "/admin/users")
			return
		}
	}

	// Revoke all refresh tokens so the role change takes effect on next re-login
	// (existing short-lived access JWTs expire within AccessExpiry).
	// M6 (pass 30): сбрасываем TTL-кэш роли — понижение применяется без ожидания
	// кэша, даже если access JWT ещё жив.
	middleware.InvalidateRoleCache(u.ID)
	if h.refreshTokenRepo != nil {
		if err := h.refreshTokenRepo.RevokeAllForUser(ctx, u.ID); err != nil {
			log.Error().Err(err).Uint("user", u.ID).Msg("ToggleAdmin: failed to revoke refresh tokens")
		}
	}

	adminID := c.GetUint("userID")
	// admin #5 (PASS-8): nil-проверка auditService (как в DeleteUser).
	if h.auditService != nil {
		h.auditService.Log(adminID, "toggle_admin_role", "user", u.ID, "new_role: "+u.Role)
	}
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
// @Failure 403 {object} map[string]interface{} "handler.forbidden"
// @Router /admin/users/{id}/delete [post]
// @Security JWT
func (h *AdminHandler) DeleteUser(c *gin.Context) {
	var req IDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		log.Warn().Err(err).Msg("DeleteUser: invalid user ID")
		render.SetFlash(c, "error", i18n.T("generic.invalid_user_id"))
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}

	adminID := c.GetUint("userID")
	if req.ID == adminID {
		render.SetFlash(c, "error", i18n.T("admin.cannot_delete_self"))
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}

	if err := h.userRepo.Delete(c.Request.Context(), req.ID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Warn().Uint("user_id", req.ID).Msg("DeleteUser: user not found")
			render.SetFlash(c, "error", i18n.T("auth.user_not_found"))
		} else {
			log.Error().Err(err).Uint("user_id", req.ID).Msg("DeleteUser: failed to delete user")
			render.SetFlash(c, "error", i18n.T("admin.user_delete_error"))
		}
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}

	// Удалённый пользователь не должен сохранять валидные refresh-токены (T-M1):
	// иначе украденный токен продолжает работать до истечения.
	// L1 (PASS-4): nil-проверка (в ToggleAdmin она есть).
	if h.refreshTokenRepo != nil {
		if err := h.refreshTokenRepo.RevokeAllForUser(c.Request.Context(), req.ID); err != nil {
			log.Error().Err(err).Uint("user_id", req.ID).Msg("DeleteUser: failed to revoke refresh tokens")
		}
	}

	// L2 (PASS-4): nil-проверка auditService.
	if h.auditService != nil {
		h.auditService.Log(adminID, "delete_user", "user", req.ID, "")
	}
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
// @Failure 403 {object} map[string]interface{} "handler.forbidden"
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
	// L-6 (pass 40): верхняя граница page - огромные OFFSET делают дорогие
	// запросы (слабая DoS-сторона для админов).
	if req.Page > 10000 {
		req.Page = 10000
	}
	if req.PerPage < 1 || req.PerPage > 100 {
		req.PerPage = 20
	}

	ctx := c.Request.Context()
	offset := (req.Page - 1) * req.PerPage
	games, total, err := h.gameRepo.AdminListGames(ctx, req.Query, req.Status, offset, req.PerPage)
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

	errorFlash := render.GetFlash(c, "error")

	render.Page(c, http.StatusOK, "admin-games.html", gin.H{
		"Title":         "Игры",
		"Games":         games,
		"Status":        req.Status,
		"Query":         req.Query,
		"Page":          req.Page,
		"PerPage":       req.PerPage,
		"TotalPages":    totalPages,
		"PrevPage":      prevPage,
		"NextPage":      nextPage,
		"Total":         total,
		"CurrentUserID": c.GetUint("userID"),
		"IsAdmin":       true,
		"Error":         errorFlash,
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
// @Failure 403 {object} map[string]interface{} "handler.forbidden"
// @Router /admin/games/{id}/delete [post]
// @Security JWT
func (h *AdminHandler) DeleteGame(c *gin.Context) {
	var req IDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		log.Warn().Err(err).Msg("DeleteGame: invalid game ID")
		render.SetFlash(c, "error", i18n.T("generic.invalid_game_id"))
		c.Redirect(http.StatusFound, "/admin/games")
		return
	}

	if h.gameService != nil {
		if err := h.gameService.AdminDelete(c.Request.Context(), req.ID); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.Warn().Uint("game_id", req.ID).Msg("DeleteGame: game not found")
				render.SetFlash(c, "error", i18n.T("game.not_found"))
			} else {
				log.Error().Err(err).Uint("game_id", req.ID).Msg("DeleteGame: failed to delete game")
				render.SetFlash(c, "error", i18n.T("admin.game_delete_error"))
			}
			c.Redirect(http.StatusFound, "/admin/games")
			return
		}
	} else {
		if err := h.gameRepo.Delete(c.Request.Context(), req.ID); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.Warn().Uint("game_id", req.ID).Msg("DeleteGame: game not found")
				render.SetFlash(c, "error", i18n.T("game.not_found"))
			} else {
				log.Error().Err(err).Uint("game_id", req.ID).Msg("DeleteGame: failed to delete game")
				render.SetFlash(c, "error", i18n.T("admin.game_delete_error"))
			}
			c.Redirect(http.StatusFound, "/admin/games")
			return
		}
	}

	// Инвалидируем кэш игры, иначе удалённая игра будет отдаваться до 5 минут.
	// (GameService.AdminDelete уже делает это при наличии сервиса — fallback для тестов).
	if h.cacheStore != nil && h.gameService == nil {
		h.cacheStore.DeleteWithCtx(c.Request.Context(), fmt.Sprintf("game:%d", req.ID))
		h.cacheStore.DeleteWithCtx(c.Request.Context(), fmt.Sprintf("rating:game:%d", req.ID))
	}

	adminID := c.GetUint("userID")
	// admin #5 (PASS-8): nil-проверка auditService.
	if h.auditService != nil {
		h.auditService.Log(adminID, "delete_game", "game", req.ID, "")
	}
	c.Redirect(http.StatusFound, "/admin/games")
}

// ---------- Команды ----------

// ListTeams отображает список всех команд.
// @Summary Список команд
// @Tags admin
// @Produce html
// @Param page query int false "Номер страницы" default(1)
// @Param per_page query int false "Количество записей на странице" default(20)
// @Success 200 {string} html "Список команд"
// @Router /admin/teams [get]
// @Security JWT
func (h *AdminHandler) ListTeams(c *gin.Context) {
	page := 1
	perPage := 20
	if p, err := strconv.Atoi(c.DefaultQuery("page", "1")); err == nil && p > 0 {
		page = p
	}
	if pp, err := strconv.Atoi(c.DefaultQuery("per_page", "20")); err == nil && pp > 0 && pp <= 100 {
		perPage = pp
	}

	query := c.DefaultQuery("query", "")
	ctx := c.Request.Context()

	var total int64
	var teams []team.Team
	var err error
	offset := (page - 1) * perPage

	if query != "" {
		total, err = h.teamRepo.CountSearch(ctx, query)
		if err != nil {
			log.Error().Err(err).Msg("ListTeams: failed to count teams search")
			render.RenderErrorPage(c, http.StatusInternalServerError)
			return
		}
		teams, err = h.teamRepo.SearchPaginated(ctx, query, offset, perPage)
		if err != nil {
			log.Error().Err(err).Msg("ListTeams: failed to search teams")
			render.RenderErrorPage(c, http.StatusInternalServerError)
			return
		}
	} else {
		total, err = h.teamRepo.Count(ctx)
		if err != nil {
			log.Error().Err(err).Msg("ListTeams: failed to count teams")
			render.RenderErrorPage(c, http.StatusInternalServerError)
			return
		}
		teams, err = h.teamRepo.ListAllPaginated(ctx, offset, perPage)
		if err != nil {
			log.Error().Err(err).Msg("ListTeams: failed to list teams")
			render.RenderErrorPage(c, http.StatusInternalServerError)
			return
		}
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
	render.Page(c, http.StatusOK, "admin-teams.html", gin.H{
		"Title":      "Команды",
		"Teams":      teams,
		"Query":      query,
		"Page":       page,
		"PerPage":    perPage,
		"TotalPages": totalPages,
		"Total":      total,
		"PrevPage":   prevPage,
		"NextPage":   nextPage,
		"csrf":       csrf.GetToken(c),
	})
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
// @Failure 403 {object} map[string]interface{} "handler.forbidden"
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
	// L-6 (pass 40): верхняя граница page - огромные OFFSET делают дорогие
	// запросы (слабая DoS-сторона для админов).
	if req.Page > 10000 {
		req.Page = 10000
	}
	if req.PerPage < 1 || req.PerPage > 100 {
		req.PerPage = 20
	}

	logs, total, err := h.auditService.List(c.Request.Context(), req.UserID, req.Action, req.Query, req.Page, req.PerPage)
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
		"Title":         "Журнал аудита",
		"Logs":          logs,
		"Page":          req.Page,
		"TotalPages":    totalPages,
		"PrevPage":      prevPage,
		"NextPage":      nextPage,
		"UserID":        req.UserID,
		"Action":        req.Action,
		"Query":         req.Query,
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
// @Failure 403 {object} map[string]interface{} "handler.forbidden"
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
		"Title":         "Резервные копии",
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
// @Failure 403 {object} map[string]interface{} "handler.forbidden"
// @Failure 500 {object} map[string]interface{} "Ошибка создания бекапа"
// @Router /admin/backups/create [post]
// @Security JWT
func (h *AdminHandler) CreateBackup(c *gin.Context) {
	// DEEP-REVIEW PASS-4 H5: CreateNow выполняет pg_dump (до 10 мин) —
	// запускаем в фоновой горутине с независимым ctx (не блокируем HTTP-ответ,
	// повторный клик не создаёт конкурирующий дамп через общий пул).
	// Параллельные CreateNow сериализуются внутри BackupService (backupMu).
	if err := h.backupService.CreateNowAsync(c.Request.Context()); err != nil {
		log.Error().Err(err).Msg("CreateBackup failed")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	// admin #5 (PASS-8): чувствительное действие — логируем в audit.
	if h.auditService != nil {
		h.auditService.Log(c.GetUint("userID"), "backup.create", "backups", 0, "")
	}
	render.SetFlash(c, "success", "Создание бекапа запущено в фоне")
	c.Redirect(http.StatusFound, "/admin/backups")
}

// DownloadBackup отдаёт файл бекапа по ID.
// @Summary Скачивание бекапа
// @Description Отдаёт файл резервной копии базы данных по ID
// @Tags admin
// @Produce application/octet-stream
// @Param id path int true "ID бекапа"
// @Success 200 {file} file "Файл бекапа"
// @Failure 400 {object} map[string]interface{} "Неверный ID"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "handler.forbidden"
// @Failure 404 {object} map[string]interface{} "Бекап не найден"
// @Router /admin/backups/{id}/download [get]
// @Security JWT
func (h *AdminHandler) DownloadBackup(c *gin.Context) {
	var req IDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		log.Warn().Err(err).Msg("DownloadBackup: invalid backup ID")
		render.RenderError(c, http.StatusBadRequest, "")
		return
	}

	path, cleanup, err := h.backupService.Download(c.Request.Context(), req.ID)
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
	// security HIGH #1 (PASS-10): удаляем временный расшифрованный файл после
	// отдачи — иначе plaintext-дамп с паролями/2FA-секретами остаётся на диске.
	if cleanup != nil {
		defer cleanup()
	}
	// admin #5 (PASS-8): скачивание полного дампа БД (пароли/2FA-секреты) —
	// чувствительное действие, логируем в audit.
	// L4 (PASS-13): в audit не пишем полный путь к расшифрованному дампу
	// (абсолютный путь = утечка метаданных в журнале) — только имя файла.
	if h.auditService != nil {
		h.auditService.Log(c.GetUint("userID"), "backup.download", "backups", req.ID, filepath.Base(path))
	}
	c.File(path)
}

// RotateBackups запускает принудительную ротацию старых бекапов.
// @Summary Ротация бекапов
// @Description Запускает принудительное удаление старых резервных копий
// @Tags admin
// @Accept x-www-form-urlencoded
// @Produce html
// @Success 302 {string} string "Перенаправление на /admin/backups"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "handler.forbidden"
// @Router /admin/backups/rotate [post]
// @Security JWT
func (h *AdminHandler) RotateBackups(c *gin.Context) {
	if err := h.backupService.RotateBackups(c.Request.Context()); err != nil {
		log.Error().Err(err).Msg("RotateBackups failed")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	// admin #5 (PASS-8): удаление бэкапов — логируем в audit.
	if h.auditService != nil {
		h.auditService.Log(c.GetUint("userID"), "backup.rotate", "backups", 0, "")
	}
	c.Redirect(http.StatusFound, "/admin/backups")
}
