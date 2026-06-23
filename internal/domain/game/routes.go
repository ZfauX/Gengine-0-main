// internal/domain/game/routes.go
package game

import (
	"net/http"
	"strconv"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/audit"
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
	coAuthorSvc *CoAuthorService,
	attemptSvc *AttemptService,
	progressSvc *LevelProgressService,
	monitorSvc *MonitorService,
	auditSvc *audit.Service,
) {
	authService := user.NewAuthService(db, cfg)
	reviewService := NewReviewService(db)
	ratingService := NewRatingService(db)
	teamService := team.NewTeamService(db)

	gameService := NewGameService(db, coAuthorSvc, reviewService, monitorSvc, hub, attemptSvc, progressSvc, cfg)
	passingService := NewGamePassingService(db, teamService, coAuthorSvc)
	noteService := NewNoteService(db, coAuthorSvc)
	simulateService := NewSimulateService(db, coAuthorSvc)
	photoService := NewPhotoService(db) // новый сервис для фото

	gameHandler := NewGameHandler(gameService, passingService, coAuthorSvc, noteService, simulateService, photoService, store, hub, auditSvc)
	reviewHandler := NewReviewHandler(reviewService)
	ratingHandler := NewRatingHandler(ratingService)

	authRequired := middleware.AuthRequired(authService)
	optionalAuth := middleware.OptionalAuth(authService) // опциональная аутентификация
	gameManager := middleware.GameManager(coAuthorSvc)
	gameOwner := ownerOnlyMiddleware(db)

	// Публичные маршруты (с опциональной аутентификацией, чтобы userID был доступен при наличии JWT)
	router.GET("/games", optionalAuth, gameHandler.List)
	router.GET("/games/:id", optionalAuth, gameHandler.Show) // показывает черновик автору
	router.GET("/ratings", ratingHandler.Leaderboard)

	protected := router.Group("/")
	protected.Use(authRequired)
	{
		protected.GET("/games/new", gameHandler.NewForm)
		protected.POST("/games", gameHandler.Create)

		// Группа для действий, требующих прав на игру (автор или соавтор)
		gameGroup := protected.Group("/games/:id")
		gameGroup.Use(gameManager)
		{
			gameGroup.GET("/edit", gameHandler.EditForm)
			gameGroup.POST("/update", gameHandler.Update)          // обновление игры
			gameGroup.POST("/publish", gameHandler.Publish)
			gameGroup.POST("/delete", gameHandler.Delete)
			gameGroup.POST("/force-finish", gameHandler.ForceFinish)
			gameGroup.POST("/disqualify", gameHandler.DisqualifyTeam)
			gameGroup.POST("/simulate", gameHandler.Simulate)

			// Настройки
			gameGroup.GET("/settings", gameHandler.SettingsPage)
			gameGroup.POST("/settings", gameHandler.SaveSettings)

			// Тестовое прохождение
			gameGroup.GET("/test", gameHandler.TestPage)

			// Фотогалерея
			gameGroup.GET("/photos", gameHandler.PhotosPage)
			gameGroup.POST("/photos", gameHandler.UploadPhoto)         // загрузка фото
			gameGroup.DELETE("/photos/:photo_id", gameHandler.DeletePhoto) // удаление фото

			// Соавторы – только владелец
			coAuthorGroup := gameGroup.Group("/co-authors")
			coAuthorGroup.Use(gameOwner)
			{
				coAuthorGroup.GET("", gameHandler.ManageCoAuthors)
				coAuthorGroup.POST("", gameHandler.AddCoAuthor)
				coAuthorGroup.POST("/:user_id/remove", gameHandler.RemoveCoAuthor)
			}
		}

		// Быстрый просмотр (требуется аутентификация)
		protected.GET("/api/v1/games/:id/full-preview", gameHandler.FullPreview)

		// Маршруты прохождений и заявок
		protected.GET("/games/:id/passings", gameHandler.ListPassings)
		protected.GET("/games/:id/apply", gameHandler.ApplyForm)
		protected.POST("/games/:id/apply", gameHandler.Apply)
		protected.POST("/games/:id/passings/:passing_id/status", gameHandler.UpdatePassingStatus)
		protected.POST("/games/:id/passings/:passing_id/start", gameHandler.StartGame)

		// Отзывы
		protected.GET("/games/:id/review", reviewHandler.ShowForm)
		protected.POST("/games/:id/review", reviewHandler.Create)

		// Заметки
		protected.GET("/api/games/:id/notes", gameHandler.Notes)
		protected.POST("/api/games/:id/notes", gameHandler.CreateNote)
		protected.DELETE("/api/notes/:note_id", gameHandler.DeleteNote)
	}
}

func RegisterGameplayRoutes(
	router *gin.RouterGroup,
	handler *GameplayHandler,
	coAuthorSvc *CoAuthorService,
) {
	codeLimiter := middleware.CodeSubmissionRateLimit(1*time.Minute, 20)

	router.GET("/game/:passing_id", handler.ShowGame)
	router.POST("/game/:passing_id", codeLimiter, handler.SubmitCode)
	router.POST("/game/:passing_id/hint", codeLimiter, handler.UseHint)
	router.POST("/game/:passing_id/file", codeLimiter, handler.SubmitFile)

	testingGroup := router.Group("/games/:id")
	testingGroup.Use(middleware.GameManager(coAuthorSvc))
	{
		testingGroup.GET("/testing/start", handler.StartTesting)
	}

	router.GET("/testing/:passing_id", handler.ShowTestGame)
	router.POST("/testing/:passing_id", handler.SubmitTestCode)
	router.POST("/testing/:passing_id/skip", handler.SkipTestLevel)

	authorGroup := router.Group("/games/:id")
	authorGroup.Use(middleware.GameManager(coAuthorSvc))
	{
		authorGroup.POST("/passings/:passing_id/accept-answer", handler.AcceptAnswer)
	}
}

func ownerOnlyMiddleware(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		gameID, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		userID := c.GetUint("userID")
		if userID == 0 {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		var g Game
		if err := db.First(&g, gameID).Error; err != nil {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		if g.AuthorID != userID {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
}