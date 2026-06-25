// internal/domain/social/routes.go
package social

import (
	"gengine-0/internal/config"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterRoutes регистрирует маршруты социальных функций: подписки.
// @tags social
func RegisterRoutes(
	router *gin.Engine,
	db *gorm.DB,
	cfg *config.Config,
	authService *user.AuthService,
) {
	followRepo := NewGormFollowRepo(db)
	followService := NewFollowService(followRepo)
	followHandler := NewFollowHandler(followService)

	authRequired := middleware.AuthRequired(authService)

	protected := router.Group("/")
	protected.Use(authRequired)
	{
		// @Summary Подписаться на автора
		// @Description Создаёт подписку текущего пользователя на автора игр
		// @Tags social
		// @Accept json
		// @Produce json
		// @Param id path int true "ID автора"
		// @Success 200 {object} map[string]interface{} "Статус followed"
		// @Failure 400 {object} map[string]interface{} "Неверный ID автора"
		// @Router /follow/{id} [post]
		// @Security JWT
		protected.POST("/follow/:id", followHandler.Follow)

		// @Summary Отписаться от автора
		// @Description Удаляет подписку текущего пользователя на автора
		// @Tags social
		// @Accept json
		// @Produce json
		// @Param id path int true "ID автора"
		// @Success 200 {object} map[string]interface{} "Статус unfollowed"
		// @Failure 400 {object} map[string]interface{} "Неверный ID автора"
		// @Router /follow/{id} [delete]
		// @Security JWT
		protected.DELETE("/follow/:id", followHandler.Unfollow)

		// @Summary Проверить подписку
		// @Description Проверяет, подписан ли текущий пользователь на автора
		// @Tags social
		// @Produce json
		// @Param id path int true "ID автора"
		// @Success 200 {object} map[string]interface{} "Статус подписки"
		// @Failure 400 {object} map[string]interface{} "Неверный ID автора"
		// @Router /follow/{id}/check [get]
		// @Security JWT
		protected.GET("/follow/:id/check", followHandler.IsFollowing)

		// @Summary Список подписок
		// @Description Отображает HTML-страницу с авторами, на которых подписан текущий пользователь
		// @Tags social
		// @Produce html
		// @Success 200 {string} html "Страница подписок"
		// @Router /subscriptions [get]
		// @Security JWT
		protected.GET("/subscriptions", followHandler.Subscriptions)
	}
}
