// internal/domain/level/routes.go
package level

import (
	"gengine-0/internal/config"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterRoutes СЂРµРіРёСЃС‚СЂРёСЂСѓРµС‚ РјР°СЂС€СЂСѓС‚С‹ РґР»СЏ СѓСЂРѕРІРЅРµР№, РІРѕРїСЂРѕСЃРѕРІ Рё РѕС‚РІРµС‚РѕРІ.
func RegisterRoutes(
	r *gin.RouterGroup,
	levelService *LevelService,
	questionService *QuestionService,
	answerService *AnswerService,
	localStorage storage.FileStorage,
	hub *ws.RoomHub,
	cfg *config.Config,
	authorizer middleware.GameAuthorizer,
	authService *user.AuthService,
	db *gorm.DB,
) {
	handler := NewLevelHandler(
		levelService,
		questionService,
		answerService,
		localStorage,
		hub,
		cfg,
		authorizer,
		db,
	)

	protected := r.Group("/games/:id/levels")
	protected.Use(middleware.AuthRequired(authService))

	// ========================================================================
	// РЈР РћР’РќР
	// ========================================================================

	protected.GET("/", handler.ListByGame)

	protected.GET("/new", handler.NewForm)

	protected.POST("/", handler.Create)

	protected.GET("/:level_id", handler.EditForm)

	protected.GET("/:level_id/edit", handler.EditForm)

	protected.POST("/:level_id/update", handler.Update)

	protected.POST("/:level_id/edit", handler.Update)

	protected.POST("/:level_id/delete", handler.Delete)

	protected.POST("/:level_id/duplicate", handler.Duplicate)

	protected.POST("/:level_id/move", handler.Move)

	// ========================================================================
	// Р’РћРџР РћРЎР«
	// ========================================================================

	questions := protected.Group("/:level_id/questions")
	{
		questions.GET("/", handler.ListQuestions)

		questions.GET("/new", handler.NewQuestionForm)

		questions.POST("/", handler.CreateQuestion)

		questions.GET("/:question_id/edit", handler.EditQuestionForm)

		questions.POST("/:question_id/edit", handler.UpdateQuestion)

		questions.POST("/:question_id", handler.UpdateQuestion)

		questions.POST("/:question_id/delete", handler.DeleteQuestion)

		// ====================================================================
		// РћРўР’Р•РўР«
		// ====================================================================

		answers := questions.Group("/:question_id/answers")
		{
			answers.GET("/", handler.ListAnswers)

			answers.POST("/", handler.CreateAnswer)

			answers.POST("/:answer_id/delete", handler.DeleteAnswer)
		}
	}
}
