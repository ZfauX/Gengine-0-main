// internal/domain/tournament/routes.go
package tournament

import (
	"gengine-0/internal/config"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes регистрирует маршруты турниров.
// @tags tournaments
func RegisterRoutes(
	r *gin.Engine,
	tournamentService *TournamentService,
	teamService *team.TeamService,
	cfg *config.Config,
	authService *user.AuthService,
) {
	handler := NewTournamentHandler(tournamentService, teamService, cfg)

	public := r.Group("/tournaments")
	{
		// @Summary Список турниров
		// @Description Возвращает HTML-страницу со списком всех турниров
		// @Tags tournaments
		// @Produce html
		// @Success 200 {string} html "Страница со списком турниров"
		// @Router /tournaments [get]
		public.GET("/", handler.List)

		// @Summary Детали турнира
		// @Description Отображает информацию о турнире, список игр и таблицу лидеров
		// @Tags tournaments
		// @Produce html
		// @Param id path int true "ID турнира"
		// @Success 200 {string} html "Страница турнира"
		// @Failure 404 {object} map[string]interface{} "Турнир не найден"
		// @Router /tournaments/{id} [get]
		public.GET("/:id", handler.Show)
	}

	protected := r.Group("/tournaments")
	protected.Use(middleware.AuthRequired(authService))
	{
		// @Summary Форма создания турнира
		// @Description Возвращает HTML-страницу с формой для создания нового турнира
		// @Tags tournaments
		// @Produce html
		// @Success 200 {string} html "Форма создания турнира"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Router /tournaments/new [get]
		// @Security JWT
		protected.GET("/new", handler.NewForm)

		// @Summary Создание турнира
		// @Description Создаёт новый турнир. Параметры начисления очков могут быть заданы (по умолчанию: 10, 7, 5, 2).
		// @Tags tournaments
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param name formData string true "Название турнира (2-200 символов)"
		// @Param description formData string false "Описание турнира (до 5000 символов)"
		// @Param points_for_first formData int false "Очков за 1 место" default(10)
		// @Param points_for_second formData int false "Очков за 2 место" default(7)
		// @Param points_for_third formData int false "Очков за 3 место" default(5)
		// @Param points_for_participation formData int false "Очков за участие" default(2)
		// @Success 302 {string} string "Перенаправление на /tournaments/{id}"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Router /tournaments [post]
		// @Security JWT
		protected.POST("/new", handler.Create)

		// @Summary Форма редактирования турнира
		// @Description Возвращает HTML-страницу с формой для редактирования турнира (доступно автору)
		// @Tags tournaments
		// @Produce html
		// @Param id path int true "ID турнира"
		// @Success 200 {string} html "Форма редактирования турнира"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав (только автор)"
		// @Failure 404 {object} map[string]interface{} "Турнир не найден"
		// @Router /tournaments/{id}/edit [get]
		// @Security JWT
		protected.GET("/:id/edit", handler.EditForm)

		// @Summary Обновление турнира
		// @Description Обновляет данные турнира (доступно только автору)
		// @Tags tournaments
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID турнира"
		// @Param name formData string false "Название турнира (2-200 символов)"
		// @Param description formData string false "Описание турнира (до 5000 символов)"
		// @Param points_for_first formData int false "Очков за 1 место"
		// @Param points_for_second formData int false "Очков за 2 место"
		// @Param points_for_third formData int false "Очков за 3 место"
		// @Param points_for_participation formData int false "Очков за участие"
		// @Success 302 {string} string "Перенаправление на /tournaments/{id}"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /tournaments/{id} [put]
		// @Security JWT
		protected.POST("/:id/edit", handler.Update)

		// @Summary Список игр турнира
		// @Description Отображает список игр, включённых в турнир, и доступные для добавления (доступно автору)
		// @Tags tournaments
		// @Produce html
		// @Param id path int true "ID турнира"
		// @Success 200 {string} html "Страница управления играми турнира"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /tournaments/{id}/games [get]
		// @Security JWT
		protected.GET("/:id/games", handler.Games)

		// @Summary Добавление игры в турнир
		// @Description Добавляет существующую игру в турнир (доступно автору турнира). Игра должна принадлежать автору турнира.
		// @Tags tournaments
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID турнира"
		// @Param game_id formData uint true "ID игры"
		// @Success 302 {string} string "Перенаправление на /tournaments/{id}/games"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /tournaments/{id}/games [post]
		// @Security JWT
		protected.POST("/:id/games/add", handler.AddGame)

		// @Summary Удаление игры из турнира
		// @Description Удаляет игру из турнира (доступно автору турнира)
		// @Tags tournaments
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID турнира"
		// @Param game_id path int true "ID игры"
		// @Success 302 {string} string "Перенаправление на /tournaments/{id}/games"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /tournaments/{id}/games/{game_id} [delete]
		// @Security JWT
		protected.POST("/:id/games/:game_id/remove", handler.RemoveGame)

		// @Summary Форма подачи заявки на турнир
		// @Description Отображает форму выбора команды для подачи заявки на участие в турнире
		// @Tags tournaments
		// @Produce html
		// @Param id path int true "ID турнира"
		// @Success 200 {string} html "Форма подачи заявки"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Router /tournaments/{id}/apply [get]
		// @Security JWT
		protected.GET("/:id/apply", handler.ApplyForm)

		// @Summary Подача заявки на турнир
		// @Description Команда подаёт заявку на участие в турнире (доступно капитану команды)
		// @Tags tournaments
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID турнира"
		// @Param team_id formData uint true "ID команды"
		// @Success 302 {string} string "Перенаправление на /tournaments/{id}"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав (только капитан)"
		// @Router /tournaments/{id}/apply [post]
		// @Security JWT
		protected.POST("/:id/apply", handler.Apply)
	}
}
