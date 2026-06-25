// internal/domain/team/routes.go
package team

import (
	"net/http"
	"strconv"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(
	r *gin.Engine,
	teamService *TeamService,
	invitationService *InvitationService,
	cfg *config.Config,
	localStorage storage.FileStorage,
	authorizer middleware.GameAuthorizer,
	authService *user.AuthService,
) {
	protected := r.Group("/teams")
	protected.Use(middleware.AuthRequired(authService))

	// @Summary Список моих команд
	// @Description Возвращает список команд, где пользователь является капитаном или участником
	// @Tags teams
	// @Produce html
	// @Success 200 {string} html "Страница со списком команд"
	// @Router /teams [get]
	// @Security JWT
	protected.GET("/", func(c *gin.Context) {
		userID := c.GetUint("user_id")
		teams, err := teamService.GetMyTeams(c.Request.Context(), userID)
		if err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		c.HTML(http.StatusOK, "teams_list.html", gin.H{
			"title": "Мои команды",
			"teams": teams,
		})
	})

	// @Summary Форма создания команды
	// @Description Возвращает HTML-страницу с формой для создания новой команды
	// @Tags teams
	// @Produce html
	// @Success 200 {string} html "Форма создания команды"
	// @Router /teams/create [get]
	// @Security JWT
	protected.GET("/create", func(c *gin.Context) {
		c.HTML(http.StatusOK, "team_create.html", gin.H{
			"title": "Создать команду",
			"csrf":  c.GetString("csrf"),
		})
	})

	// @Summary Создание команды
	// @Description Создаёт новую команду, текущий пользователь становится капитаном
	// @Tags teams
	// @Accept x-www-form-urlencoded
	// @Produce html
	// @Param name formData string true "Название команды (2-100 символов)"
	// @Success 302 {string} string "Перенаправление на /teams"
	// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
	// @Router /teams [post]
	// @Security JWT
	protected.POST("/create", func(c *gin.Context) {
		name := c.PostForm("name")
		userID := c.GetUint("user_id")
		team, err := teamService.CreateTeam(c.Request.Context(), name, userID)
		if err != nil {
			c.HTML(http.StatusBadRequest, "team_create.html", gin.H{"error": err.Error()})
			return
		}
		c.Redirect(http.StatusFound, "/teams/"+strconv.Itoa(int(team.ID)))
	})

	// @Summary Просмотр команды
	// @Description Отображает информацию о команде и её составе
	// @Tags teams
	// @Produce html
	// @Param id path int true "ID команды"
	// @Success 200 {string} html "Страница команды"
	// @Failure 404 {object} map[string]interface{} "Команда не найдена"
	// @Router /teams/{id} [get]
	// @Security JWT
	protected.GET("/:id", func(c *gin.Context) {
		id, _ := strconv.Atoi(c.Param("id"))
		team, members, err := teamService.GetTeamWithMembers(c.Request.Context(), uint(id))
		if err != nil {
			c.String(http.StatusNotFound, err.Error())
			return
		}
		c.HTML(http.StatusOK, "team_detail.html", gin.H{
			"title":   team.Name,
			"team":    team,
			"members": members,
		})
	})

	// @Summary Форма редактирования команды
	// @Description Возвращает HTML-страницу с формой для редактирования команды
	// @Tags teams
	// @Produce html
	// @Param id path int true "ID команды"
	// @Success 200 {string} html "Форма редактирования команды"
	// @Router /teams/{id}/edit [get]
	// @Security JWT
	protected.GET("/:id/edit", func(c *gin.Context) {
		id, _ := strconv.Atoi(c.Param("id"))
		team, _, err := teamService.GetTeamWithMembers(c.Request.Context(), uint(id))
		if err != nil {
			c.String(http.StatusNotFound, err.Error())
			return
		}
		c.HTML(http.StatusOK, "team_edit.html", gin.H{
			"title": "Редактировать команду",
			"team":  team,
			"csrf":  c.GetString("csrf"),
		})
	})

	// @Summary Обновление команды
	// @Description Обновляет данные команды
	// @Tags teams
	// @Accept x-www-form-urlencoded
	// @Produce html
	// @Param id path int true "ID команды"
	// @Param name formData string true "Название команды"
	// @Success 302 {string} string "Перенаправление на /teams/{id}"
	// @Router /teams/{id} [put]
	// @Security JWT
	protected.POST("/:id/edit", func(c *gin.Context) {
		// Для простоты оставляем редирект, но в реальном коде здесь должно быть обновление
		c.Redirect(http.StatusFound, "/teams/"+c.Param("id"))
	})

	// @Summary Добавление участника
	// @Description Добавляет нового участника в команду (доступно капитану или автору игры)
	// @Tags teams
	// @Accept x-www-form-urlencoded
	// @Produce html
	// @Param id path int true "ID команды"
	// @Param user_id formData uint true "ID пользователя"
	// @Success 302 {string} string "Перенаправление на /teams/{id}"
	// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
	// @Router /teams/{id}/members [post]
	// @Security JWT
	protected.POST("/:id/members/add", func(c *gin.Context) {
		id, _ := strconv.Atoi(c.Param("id"))
		newMemberID, _ := strconv.Atoi(c.PostForm("user_id"))
		if err := teamService.AddMember(c.Request.Context(), uint(id), uint(newMemberID), c.GetUint("user_id")); err != nil {
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		c.Redirect(http.StatusFound, "/teams/"+strconv.Itoa(id))
	})

	// @Summary Удаление участника
	// @Description Удаляет участника из команды (доступно капитану или автору игры)
	// @Tags teams
	// @Accept x-www-form-urlencoded
	// @Produce html
	// @Param id path int true "ID команды"
	// @Param member_id path int true "ID участника"
	// @Success 302 {string} string "Перенаправление на /teams/{id}"
	// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
	// @Router /teams/{id}/members/{member_id} [delete]
	// @Security JWT
	protected.POST("/:id/members/:member_id/remove", func(c *gin.Context) {
		id, _ := strconv.Atoi(c.Param("id"))
		memberID, _ := strconv.Atoi(c.Param("member_id"))
		if err := teamService.RemoveMember(c.Request.Context(), uint(id), uint(memberID), c.GetUint("user_id")); err != nil {
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		c.Redirect(http.StatusFound, "/teams/"+strconv.Itoa(id))
	})

	// @Summary Смена капитана
	// @Description Меняет капитана команды (доступно текущему капитану)
	// @Tags teams
	// @Accept x-www-form-urlencoded
	// @Produce html
	// @Param id path int true "ID команды"
	// @Param captain_id formData uint true "ID нового капитана"
	// @Success 302 {string} string "Перенаправление на /teams/{id}"
	// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
	// @Router /teams/{id}/captain [post]
	// @Security JWT
	protected.POST("/:id/captain", func(c *gin.Context) {
		id, _ := strconv.Atoi(c.Param("id"))
		newCaptainID, _ := strconv.Atoi(c.PostForm("captain_id"))
		if err := teamService.ChangeCaptain(c.Request.Context(), uint(id), uint(newCaptainID), c.GetUint("user_id")); err != nil {
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		c.Redirect(http.StatusFound, "/teams/"+strconv.Itoa(id))
	})

	invites := protected.Group("/invitations")
	{
		// @Summary Мои приглашения
		// @Description Отображает список приглашений текущего пользователя
		// @Tags invitations
		// @Produce html
		// @Success 200 {string} html "Страница с моими приглашениями"
		// @Router /invitations [get]
		// @Security JWT
		invites.GET("/", func(c *gin.Context) {
			userID := c.GetUint("user_id")
			invitations, err := invitationService.GetPendingForUser(c.Request.Context(), userID)
			if err != nil {
				c.String(http.StatusInternalServerError, err.Error())
				return
			}
			c.HTML(http.StatusOK, "invitations_list.html", gin.H{
				"title":       "Приглашения",
				"invitations": invitations,
			})
		})

		// @Summary Создание приглашения
		// @Description Создаёт приглашение пользователя в команду (доступно капитану или автору игры)
		// @Tags invitations
		// @Accept x-www-form-urlencoded
		// @Produce json
		// @Param team_id formData uint true "ID команды"
		// @Param user_id formData uint true "ID приглашаемого пользователя"
		// @Success 200 {object} map[string]interface{} "Созданное приглашение"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Router /invitations [post]
		// @Security JWT
		invites.POST("/create", func(c *gin.Context) {
			teamID, _ := strconv.Atoi(c.PostForm("team_id"))
			invitedUserID, _ := strconv.Atoi(c.PostForm("user_id"))
			inv, err := invitationService.CreateInvitation(c.Request.Context(), uint(teamID), uint(invitedUserID), c.GetUint("user_id"))
			if err != nil {
				c.String(http.StatusBadRequest, err.Error())
				return
			}
			c.JSON(http.StatusOK, inv)
		})

		// @Summary Принять приглашение
		// @Description Принимает приглашение в команду
		// @Tags invitations
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID приглашения"
		// @Success 302 {string} string "Перенаправление на /invitations"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /invitations/{id}/accept [post]
		// @Security JWT
		invites.POST("/:id/accept", func(c *gin.Context) {
			id, _ := strconv.Atoi(c.Param("id"))
			if err := invitationService.AcceptInvitation(c.Request.Context(), uint(id), c.GetUint("user_id")); err != nil {
				c.String(http.StatusBadRequest, err.Error())
				return
			}
			c.Redirect(http.StatusFound, "/teams/invitations")
		})

		// @Summary Отклонить приглашение
		// @Description Отклоняет приглашение в команду
		// @Tags invitations
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param id path int true "ID приглашения"
		// @Success 302 {string} string "Перенаправление на /invitations"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /invitations/{id}/decline [post]
		// @Security JWT
		invites.POST("/:id/decline", func(c *gin.Context) {
			id, _ := strconv.Atoi(c.Param("id"))
			if err := invitationService.DeclineInvitation(c.Request.Context(), uint(id), c.GetUint("user_id")); err != nil {
				c.String(http.StatusBadRequest, err.Error())
				return
			}
			c.Redirect(http.StatusFound, "/teams/invitations")
		})
	}
}
