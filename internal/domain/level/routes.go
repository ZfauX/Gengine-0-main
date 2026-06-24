// internal/domain/level/routes.go
package level

import (
	"net/http"
	"strconv"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(
	r *gin.Engine,
	levelService *LevelService,
	questionService *QuestionService,
	answerService *AnswerService,
	localStorage storage.FileStorage,
	hub *ws.RoomHub,
	cfg *config.Config,
	authorizer middleware.GameAuthorizer,
	activeGameManager ActiveGameManager,
	authService *user.AuthService,
) {
	protected := r.Group("/games/:game_id/levels")
	protected.Use(middleware.AuthRequired(authService))

	protected.GET("/", func(c *gin.Context) {
		gameID, _ := strconv.Atoi(c.Param("game_id"))
		levels, err := levelService.ListByGame(c.Request.Context(), uint(gameID))
		if err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		c.HTML(http.StatusOK, "levels_list.html", gin.H{
			"title":  "Уровни",
			"levels": levels,
			"gameID": gameID,
		})
	})

	protected.GET("/create", func(c *gin.Context) {
		gameID, _ := strconv.Atoi(c.Param("game_id"))
		c.HTML(http.StatusOK, "level_create.html", gin.H{
			"title":  "Создать уровень",
			"gameID": gameID,
			"csrf":   c.GetString("csrf"),
		})
	})
	protected.POST("/create", func(c *gin.Context) {
		gameID, _ := strconv.Atoi(c.Param("game_id"))
		var level Level
		if err := c.ShouldBind(&level); err != nil {
			c.HTML(http.StatusBadRequest, "level_create.html", gin.H{"error": err.Error()})
			return
		}
		if err := levelService.Create(c.Request.Context(), uint(gameID), &level, c.GetUint("user_id")); err != nil {
			c.HTML(http.StatusBadRequest, "level_create.html", gin.H{"error": err.Error()})
			return
		}
		c.Redirect(http.StatusFound, "/games/"+strconv.Itoa(gameID)+"/levels")
	})

	protected.GET("/:level_id", func(c *gin.Context) {
		levelID, _ := strconv.Atoi(c.Param("level_id"))
		level, err := levelService.GetByID(c.Request.Context(), uint(levelID))
		if err != nil {
			c.String(http.StatusNotFound, err.Error())
			return
		}
		c.HTML(http.StatusOK, "level_detail.html", gin.H{
			"title": level.Name,
			"level": level,
		})
	})

	protected.GET("/:level_id/edit", func(c *gin.Context) {
		levelID, _ := strconv.Atoi(c.Param("level_id"))
		level, err := levelService.GetByID(c.Request.Context(), uint(levelID))
		if err != nil {
			c.String(http.StatusNotFound, err.Error())
			return
		}
		c.HTML(http.StatusOK, "level_edit.html", gin.H{
			"title": "Редактировать уровень",
			"level": level,
			"csrf":  c.GetString("csrf"),
		})
	})
	protected.POST("/:level_id/edit", func(c *gin.Context) {
		levelID, _ := strconv.Atoi(c.Param("level_id"))
		var updated Level
		if err := c.ShouldBind(&updated); err != nil {
			c.HTML(http.StatusBadRequest, "level_edit.html", gin.H{"error": err.Error()})
			return
		}
		if err := levelService.Update(c.Request.Context(), uint(levelID), &updated, c.GetUint("user_id")); err != nil {
			c.HTML(http.StatusBadRequest, "level_edit.html", gin.H{"error": err.Error()})
			return
		}
		c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/levels/"+strconv.Itoa(levelID))
	})

	protected.POST("/:level_id/delete", func(c *gin.Context) {
		levelID, _ := strconv.Atoi(c.Param("level_id"))
		gameID, _ := strconv.Atoi(c.Param("game_id"))
		if err := levelService.DeleteFromActiveGame(c.Request.Context(), uint(gameID), uint(levelID), c.GetUint("user_id")); err != nil {
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		c.Redirect(http.StatusFound, "/games/"+strconv.Itoa(gameID)+"/levels")
	})

	protected.POST("/:level_id/duplicate", func(c *gin.Context) {
		levelID, _ := strconv.Atoi(c.Param("level_id"))
		newLevel, err := levelService.Duplicate(c.Request.Context(), uint(levelID), c.GetUint("user_id"))
		if err != nil {
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/levels/"+strconv.Itoa(int(newLevel.ID)))
	})

	protected.POST("/:level_id/move", func(c *gin.Context) {
		levelID, _ := strconv.Atoi(c.Param("level_id"))
		direction := c.PostForm("direction")
		if err := levelService.Move(c.Request.Context(), uint(levelID), direction, c.GetUint("user_id")); err != nil {
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/levels")
	})

	questionGroup := protected.Group("/:level_id/questions")
	{
		questionGroup.GET("/", func(c *gin.Context) {
			levelID, _ := strconv.Atoi(c.Param("level_id"))
			questions, err := questionService.ListByLevel(c.Request.Context(), uint(levelID))
			if err != nil {
				c.String(http.StatusInternalServerError, err.Error())
				return
			}
			c.HTML(http.StatusOK, "questions_list.html", gin.H{
				"title":     "Вопросы",
				"questions": questions,
				"levelID":   levelID,
			})
		})
		questionGroup.GET("/create", func(c *gin.Context) {
			levelID, _ := strconv.Atoi(c.Param("level_id"))
			c.HTML(http.StatusOK, "question_create.html", gin.H{
				"title":   "Создать вопрос",
				"levelID": levelID,
				"csrf":    c.GetString("csrf"),
			})
		})
		questionGroup.POST("/create", func(c *gin.Context) {
			levelID, _ := strconv.Atoi(c.Param("level_id"))
			var question Question
			if err := c.ShouldBind(&question); err != nil {
				c.HTML(http.StatusBadRequest, "question_create.html", gin.H{"error": err.Error()})
				return
			}
			if err := questionService.Create(c.Request.Context(), uint(levelID), &question, c.GetUint("user_id")); err != nil {
				c.HTML(http.StatusBadRequest, "question_create.html", gin.H{"error": err.Error()})
				return
			}
			c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/levels/"+c.Param("level_id")+"/questions")
		})
		questionGroup.GET("/:question_id/edit", func(c *gin.Context) {
			questionID, _ := strconv.Atoi(c.Param("question_id"))
			question, err := questionService.GetByID(c.Request.Context(), uint(questionID))
			if err != nil {
				c.String(http.StatusNotFound, err.Error())
				return
			}
			c.HTML(http.StatusOK, "question_edit.html", gin.H{
				"title":    "Редактировать вопрос",
				"question": question,
				"csrf":     c.GetString("csrf"),
			})
		})
		questionGroup.POST("/:question_id/edit", func(c *gin.Context) {
			questionID, _ := strconv.Atoi(c.Param("question_id"))
			var updated Question
			if err := c.ShouldBind(&updated); err != nil {
				c.HTML(http.StatusBadRequest, "question_edit.html", gin.H{"error": err.Error()})
				return
			}
			if err := questionService.Update(c.Request.Context(), uint(questionID), &updated, c.GetUint("user_id")); err != nil {
				c.HTML(http.StatusBadRequest, "question_edit.html", gin.H{"error": err.Error()})
				return
			}
			c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/levels/"+c.Param("level_id")+"/questions")
		})
		questionGroup.POST("/:question_id/delete", func(c *gin.Context) {
			questionID, _ := strconv.Atoi(c.Param("question_id"))
			if err := questionService.Delete(c.Request.Context(), uint(questionID), c.GetUint("user_id")); err != nil {
				c.String(http.StatusBadRequest, err.Error())
				return
			}
			c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/levels/"+c.Param("level_id")+"/questions")
		})

		answerGroup := questionGroup.Group("/:question_id/answers")
		{
			answerGroup.GET("/", func(c *gin.Context) {
				questionID, _ := strconv.Atoi(c.Param("question_id"))
				answers, err := answerService.ListByQuestion(c.Request.Context(), uint(questionID))
				if err != nil {
					c.String(http.StatusInternalServerError, err.Error())
					return
				}
				c.HTML(http.StatusOK, "answers_list.html", gin.H{
					"title":      "Ответы",
					"answers":    answers,
					"questionID": questionID,
				})
			})
			answerGroup.POST("/create", func(c *gin.Context) {
				questionID, _ := strconv.Atoi(c.Param("question_id"))
				var answer Answer
				if err := c.ShouldBind(&answer); err != nil {
					c.String(http.StatusBadRequest, err.Error())
					return
				}
				if err := answerService.Create(c.Request.Context(), uint(questionID), &answer, c.GetUint("user_id")); err != nil {
					c.String(http.StatusBadRequest, err.Error())
					return
				}
				c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/levels/"+c.Param("level_id")+"/questions/"+c.Param("question_id")+"/answers")
			})
			answerGroup.POST("/:answer_id/delete", func(c *gin.Context) {
				answerID, _ := strconv.Atoi(c.Param("answer_id"))
				if err := answerService.Delete(c.Request.Context(), uint(answerID), c.GetUint("user_id")); err != nil {
					c.String(http.StatusBadRequest, err.Error())
					return
				}
				c.Redirect(http.StatusFound, "/games/"+c.Param("game_id")+"/levels/"+c.Param("level_id")+"/questions/"+c.Param("question_id")+"/answers")
			})
		}
	}
}
