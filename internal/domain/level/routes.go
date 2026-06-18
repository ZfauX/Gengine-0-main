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

func RegisterRoutes(
	router *gin.Engine,
	db *gorm.DB,
	store storage.FileStorage,
	hub *ws.RoomHub,
	cfg *config.Config,
	coAuthorSvc middleware.GameAuthorizer,
	activeGameManager ActiveGameManager,
) {
	authService := user.NewAuthService(db, cfg)

	levelService := NewLevelService(db, coAuthorSvc, activeGameManager)
	questionService := NewQuestionService(db, coAuthorSvc)
	answerService := NewAnswerService(db, coAuthorSvc)

	levelHandler := NewLevelHandler(levelService, questionService, answerService, store, hub)
	questionHandler := NewQuestionHandler(questionService)
	answerHandler := NewAnswerHandler(answerService)

	authRequired := middleware.AuthRequired(authService)
	gameManager := middleware.GameManager(coAuthorSvc)

	protected := router.Group("/")
	protected.Use(authRequired)

	// Уровни – требуют прав на игру
	levelGroup := protected.Group("/games/:id/levels")
	levelGroup.Use(gameManager)
	{
		levelGroup.GET("", levelHandler.ListLevels)
		levelGroup.GET("/new", levelHandler.NewLevelForm)
		levelGroup.POST("", levelHandler.CreateLevel)
		levelGroup.GET("/:level_id", levelHandler.ShowLevel)                 // <-- вот этот маршрут должен обрабатывать GET /games/:id/levels/:level_id
		levelGroup.GET("/:level_id/edit", levelHandler.EditLevelForm)
		levelGroup.POST("/:level_id/update", levelHandler.UpdateLevel)
		levelGroup.POST("/:level_id/delete", levelHandler.DeleteLevel)
		levelGroup.POST("/:level_id/duplicate", levelHandler.DuplicateLevel)
		levelGroup.POST("/:level_id/move", levelHandler.MoveLevel)

		// Вопросы
		levelGroup.GET("/:level_id/questions", questionHandler.ListQuestions)
		levelGroup.GET("/:level_id/questions/new", questionHandler.NewQuestionForm)
		levelGroup.POST("/:level_id/questions", questionHandler.CreateQuestion)
		levelGroup.GET("/:level_id/questions/:question_id", questionHandler.ShowQuestion)
		levelGroup.GET("/:level_id/questions/:question_id/edit", questionHandler.EditQuestionForm)
		levelGroup.POST("/:level_id/questions/:question_id/update", questionHandler.UpdateQuestion)
		levelGroup.POST("/:level_id/questions/:question_id/delete", questionHandler.DeleteQuestion)

		// Ответы
		levelGroup.GET("/:level_id/questions/:question_id/answers", answerHandler.Index)
		levelGroup.POST("/:level_id/questions/:question_id/answers", answerHandler.Create)
		levelGroup.POST("/:level_id/questions/:question_id/answers/:answer_id/delete", answerHandler.Delete)
	}
}