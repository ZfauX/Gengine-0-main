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

	protected.GET("/create", func(c *gin.Context) {
		c.HTML(http.StatusOK, "team_create.html", gin.H{
			"title": "Создать команду",
			"csrf":  c.GetString("csrf"),
		})
	})
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
	protected.POST("/:id/edit", func(c *gin.Context) {
		id, _ := strconv.Atoi(c.Param("id"))
		// Имя не используется — убираем ненужную переменную
		// name := c.PostForm("name") // закомментировано или удалено
		// Вместо этого просто редирект (если нужно обновление — реализовать отдельно)
		c.Redirect(http.StatusFound, "/teams/"+strconv.Itoa(id))
	})

	protected.POST("/:id/members/add", func(c *gin.Context) {
		id, _ := strconv.Atoi(c.Param("id"))
		newMemberID, _ := strconv.Atoi(c.PostForm("user_id"))
		if err := teamService.AddMember(c.Request.Context(), uint(id), uint(newMemberID), c.GetUint("user_id")); err != nil {
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		c.Redirect(http.StatusFound, "/teams/"+strconv.Itoa(id))
	})

	protected.POST("/:id/members/:member_id/remove", func(c *gin.Context) {
		id, _ := strconv.Atoi(c.Param("id"))
		memberID, _ := strconv.Atoi(c.Param("member_id"))
		if err := teamService.RemoveMember(c.Request.Context(), uint(id), uint(memberID), c.GetUint("user_id")); err != nil {
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		c.Redirect(http.StatusFound, "/teams/"+strconv.Itoa(id))
	})

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

		invites.POST("/:id/accept", func(c *gin.Context) {
			id, _ := strconv.Atoi(c.Param("id"))
			if err := invitationService.AcceptInvitation(c.Request.Context(), uint(id), c.GetUint("user_id")); err != nil {
				c.String(http.StatusBadRequest, err.Error())
				return
			}
			c.Redirect(http.StatusFound, "/teams/invitations")
		})

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
