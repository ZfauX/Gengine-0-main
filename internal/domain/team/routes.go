// internal/domain/team/routes.go
package team

import (
	"gengine-0/internal/config"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes регистрирует маршруты команд и приглашений.
// @tags teams
// @tags invitations
func RegisterRoutes(
	r *gin.RouterGroup,
	teamService *TeamService,
	invitationService *InvitationService,
	cfg *config.Config,
	localStorage storage.FileStorage,
	authorizer middleware.GameAuthorizer,
	authService *user.AuthService,
) {
	teamHandler := NewTeamHandler(teamService, localStorage)
	invitationHandler := NewInvitationHandler(invitationService)

	teamsGroup := r.Group("/teams")
	teamsGroup.Use(middleware.AuthRequired(authService))
	{
		// @Summary Список моих команд
		// @Description Возвращает HTML-страницу со списком команд, где пользователь является капитаном или участником
		// @Tags teams
		// @Produce html
		// @Success 200 {string} html "Страница со списком команд"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Router /teams [get]
		// @Security JWT
		teamsGroup.GET("/", teamHandler.MyTeams)

		// @Summary Форма создания команды
		// @Description Возвращает HTML-страницу с формой для создания новой команды
		// @Tags teams
		// @Produce html
		// @Success 200 {string} html "Форма создания команды"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Router /teams/new [get]
		// @Security JWT
		teamsGroup.GET("/new", teamHandler.NewTeamForm)

		// @Summary Создание команды
		// @Description Создаёт новую команду, текущий пользователь становится капитаном. Название должно быть уникальным и длиной от 2 до 100 символов.
		// @Tags teams
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param name formData string true "Название команды (2-100 символов)"
		// @Success 302 {string} string "Перенаправление на /teams"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Router /teams [post]
		// @Security JWT
		teamsGroup.POST("/new", teamHandler.CreateTeam)

		// @Summary Просмотр команды
		// @Description Отображает информацию о команде и её составе (капитан, участники)
		// @Tags teams
		// @Produce html
		// @Param team_id path int true "ID команды"
		// @Success 200 {string} html "Страница команды"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 404 {object} map[string]interface{} "Команда не найдена"
		// @Router /teams/{team_id} [get]
		// @Security JWT
		teamsGroup.GET("/:team_id", teamHandler.ViewTeam)
	}

	invitationsGroup := r.Group("/invitations")
	invitationsGroup.Use(middleware.AuthRequired(authService))
	{
		// @Summary Мои приглашения
		// @Description Отображает HTML-страницу со списком приглашений текущего пользователя (входящие)
		// @Tags invitations
		// @Produce html
		// @Success 200 {string} html "Страница с моими приглашениями"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Router /invitations/my [get]
		// @Security JWT
		invitationsGroup.GET("/my", invitationHandler.MyInvitations)

		// @Summary Принять приглашение
		// @Description Принимает приглашение в команду, пользователь становится участником команды
		// @Tags invitations
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID приглашения"
		// @Success 302 {string} string "Перенаправление на /invitations/my"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав (только приглашённый пользователь)"
		// @Router /invitations/{id}/accept [post]
		// @Security JWT
		invitationsGroup.POST("/:id/accept", invitationHandler.Accept)

		// @Summary Отклонить приглашение
		// @Description Отклоняет приглашение в команду
		// @Tags invitations
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID приглашения"
		// @Success 302 {string} string "Перенаправление на /invitations/my"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав (только приглашённый пользователь)"
		// @Router /invitations/{id}/decline [post]
		// @Security JWT
		invitationsGroup.POST("/:id/decline", invitationHandler.Decline)
	}
}
