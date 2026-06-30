// internal/domain/admin/routes.go
package admin

import (
	"net/http"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/audit"
	"gengine-0/internal/pkg/middleware"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterRoutes регистрирует маршруты административной панели.
// @tags admin
func RegisterRoutes(
	router *gin.Engine,
	db *gorm.DB,
	cfg *config.Config,
	authService *user.AuthService,
	userRepo user.UserRepository,
	gameRepo game.GameRepository,
) *audit.Service {
	auditService := audit.NewService(db)

	backupRepo := NewGormBackupRepo(db)
	backupService := NewBackupService(backupRepo, "backups", cfg.Server.MaxBackups, cfg.Database)

	adminHandler := NewAdminHandler(userRepo, gameRepo, backupService, auditService)

	authRequired := middleware.AuthRequired(authService)
	adminOnly := adminOnlyMiddleware()

	protected := router.Group("/admin")
	protected.Use(authRequired, adminOnly)
	{
		// @Summary Панель управления администратора
		// @Description Отображает главную страницу админ-панели с общей статистикой
		// @Tags admin
		// @Produce html
		// @Success 200 {string} html "Страница админ-панели"
		// @Router /admin [get]
		// @Security JWT
		protected.GET("/", adminHandler.Dashboard)

		// @Summary Список пользователей
		// @Description Отображает список всех пользователей с фильтром по роли
		// @Tags admin
		// @Produce html
		// @Param role query string false "Роль пользователя (user, admin)"
		// @Success 200 {string} html "Страница со списком пользователей"
		// @Router /admin/users [get]
		// @Security JWT
		protected.GET("/users", adminHandler.ListUsers)

		// @Summary Переключение роли пользователя
		// @Description Делает пользователя администратором или обычным пользователем
		// @Tags admin
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID пользователя"
		// @Success 302 {string} string "Перенаправление на /admin/users"
		// @Router /admin/users/{id}/toggle-admin [post]
		// @Security JWT
		protected.POST("/users/:id/toggle-admin", adminHandler.ToggleAdmin)

		// @Summary Удаление пользователя
		// @Description Безвозвратно удаляет пользователя (административное действие)
		// @Tags admin
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID пользователя"
		// @Success 302 {string} string "Перенаправление на /admin/users"
		// @Router /admin/users/{id}/delete [post]
		// @Security JWT
		protected.POST("/users/:id/delete", adminHandler.DeleteUser)

		// @Summary Список игр (административный)
		// @Description Отображает все игры с фильтром по статусу (черновик / опубликована)
		// @Tags admin
		// @Produce html
		// @Param status query string false "Статус игры (draft, published)"
		// @Success 200 {string} html "Страница со списком игр"
		// @Router /admin/games [get]
		// @Security JWT
		protected.GET("/games", adminHandler.ListGames)

		// @Summary Удаление игры (административное)
		// @Description Безвозвратно удаляет игру (доступно только администратору)
		// @Tags admin
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 302 {string} string "Перенаправление на /admin/games"
		// @Router /admin/games/{id}/delete [post]
		// @Security JWT
		protected.POST("/games/:id/delete", adminHandler.DeleteGame)

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
		protected.GET("/audit", adminHandler.AuditLog)

		// @Summary Список бекапов
		// @Description Отображает список созданных резервных копий базы данных
		// @Tags admin
		// @Produce html
		// @Success 200 {string} html "Страница бекапов"
		// @Router /admin/backups [get]
		// @Security JWT
		protected.GET("/backups", adminHandler.ListBackups)

		// @Summary Создание бекапа
		// @Description Создаёт новую резервную копию базы данных с помощью pg_dump
		// @Tags admin
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Success 302 {string} string "Перенаправление на /admin/backups"
		// @Router /admin/backups/create [post]
		// @Security JWT
		protected.POST("/backups/create", adminHandler.CreateBackup)

		// @Summary Скачать бекап
		// @Description Скачивает файл резервной копии по ID
		// @Tags admin
		// @Produce application/octet-stream
		// @Param id path int true "ID бекапа"
		// @Success 200 {file} file "Файл бекапа"
		// @Failure 404 {object} map[string]interface{} "Бекап не найден"
		// @Router /admin/backups/{id}/download [get]
		// @Security JWT
		protected.GET("/backups/:id/download", adminHandler.DownloadBackup)

		// @Summary Ротация бекапов
		// @Description Удаляет старые резервные копии, оставляя не более MaxBackups
		// @Tags admin
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Success 302 {string} string "Перенаправление на /admin/backups"
		// @Router /admin/backups/rotate [post]
		// @Security JWT
		protected.POST("/backups/rotate", adminHandler.RotateBackups)
	}

	return auditService
}

// adminOnlyMiddleware проверяет, что пользователь является администратором, используя роль из контекста (из JWT).
// Не требует передачи *gorm.DB, так как роль уже сохранена в контексте middleware.AuthRequired.
func adminOnlyMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, exists := c.Get("role")
		if !exists {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		roleStr, ok := role.(string)
		if !ok || roleStr != "admin" {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Set("IsAdmin", true)
		c.Next()
	}
}
