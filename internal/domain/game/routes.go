// internal/domain/game/routes.go
package game

import (
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/audit"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/gin-gonic/gin"
)

// GameDeps содержит все зависимости для регистрации маршрутов игрового домена.
// Позволяет избежать передачи 16+ параметров в RegisterRoutes.
type GameDeps struct {
	GameService     *GameService
	PassingService  *GamePassingService
	CoAuthorSvc     *CoAuthorService
	ProgressSvc     *LevelProgressService
	MonitorSvc      *MonitorService
	SimulateSvc     *SimulateService
	LocalStorage    storage.FileStorage
	Hub             *ws.RoomHub
	Cfg             *config.Config
	AuditSvc        *audit.Service
	AuthService     *user.AuthService
	GamePlaySvc     *GamePlayService
	GameAdminSvc    *GameAdminService
	ReviewService   *ReviewService
	GameplayHandler *GameplayHandler
	PhotoService    *PhotoService
	LevelService    *level.LevelService
}

// RegisterRoutes регистрирует маршруты для игр, используя готовые обработчики.
func RegisterRoutes(r *gin.RouterGroup, deps *GameDeps) {
	gameService := deps.GameService
	passingService := deps.PassingService
	coAuthorSvc := deps.CoAuthorSvc
	localStorage := deps.LocalStorage
	auditSvc := deps.AuditSvc
	authService := deps.AuthService
	gameAdminSvc := deps.GameAdminSvc
	reviewService := deps.ReviewService
	gameplayHandler := deps.GameplayHandler
	photoService := deps.PhotoService
	levelService := deps.LevelService
	simulateService := deps.SimulateSvc

	gameHandler := NewGameHandler(
		gameService,
		coAuthorSvc,
		auditSvc,
	)

	// Создаём специализированные обработчики для каждого поддомена
	passingHandler := NewPassingHandler(
		passingService,
		gameAdminSvc,
		coAuthorSvc,
		auditSvc,
		localStorage,
	)
	coAuthorHandler := NewCoAuthorHandler(coAuthorSvc, auditSvc)
	settingsHandler := NewSettingsHandler(gameService, coAuthorSvc)
	testHandler := NewTestHandler(gameService, passingService)
	photoHandler := NewPhotoHandler(gameService, coAuthorSvc, photoService, localStorage)
	simulateHandler := NewSimulateHandler(simulateService)
	fullPreviewHandler := NewFullPreviewHandler(gameService, levelService)

	// Autocomplete handler
	autocompleteHandler := NewAutocompleteHandler(deps.GameService)
	gameStatsHandler := NewGameStatsHandler(gameService, deps.GamePlaySvc)

	// ReviewHandler для отзывов
	reviewHandler := NewReviewHandler(reviewService)

	// ========================================================================
	// Публичные маршруты с ОПЦИОНАЛЬНОЙ аутентификацией
	// ========================================================================
	optionalAuth := r.Group("/games")
	optionalAuth.Use(middleware.OptionalAuth(authService))
	{
		optionalAuth.GET("/", gameHandler.List)

		optionalAuth.GET("/:id", gameHandler.Show)
	}

	// ========================================================================
	// Защищённые маршруты (требуют обязательной аутентификации)
	// ========================================================================
	protected := r.Group("/games")
	protected.Use(middleware.AuthRequired(authService))
	{
		protected.GET("/:id/full-preview", fullPreviewHandler.FullPreview)

		protected.GET("/new", gameHandler.NewForm)

		protected.POST("/new", gameHandler.Create)

		protected.GET("/:id/edit", gameHandler.EditForm)

		protected.POST("/:id/edit", gameHandler.Update)

		protected.POST("/:id/delete", gameHandler.Delete)

		protected.POST("/:id/publish", gameHandler.Publish)

		protected.POST("/:id/force-finish", passingHandler.ForceFinish)

		protected.POST("/:id/disqualify", passingHandler.DisqualifyTeam)

		protected.GET("/:id/co-authors", coAuthorHandler.ManageCoAuthors)

		protected.POST("/:id/co-authors", coAuthorHandler.AddCoAuthor)

		protected.POST("/:id/co-authors/:user_id/delete", coAuthorHandler.RemoveCoAuthor)

		protected.GET("/:id/passings", passingHandler.ListPassings)

		protected.POST("/:id/passings/:passing_id/status", passingHandler.UpdatePassingStatus)

		protected.POST("/:id/passings/:passing_id/start", passingHandler.StartGame)

		// Фаза 3 (C-1..C-5, pass 45): маршруты, индивидуальный старт, ответы, итоги.
		protected.POST("/:id/passings/:passing_id/route", passingHandler.SetTeamRoute)
		protected.GET("/:id/passings/:passing_id/route", passingHandler.GetTeamRoute)
		protected.POST("/:id/passings/:passing_id/start-time", passingHandler.SetTeamStartTime)
		protected.POST("/:id/levels/:level_id/teams/:team_id/answer", passingHandler.SetTeamAnswer)
		protected.GET("/:id/attempts-per-user", passingHandler.AttemptsPerUser)

		protected.GET("/:id/apply", passingHandler.ApplyForm)

		protected.POST("/:id/apply", passingHandler.Apply)

		protected.GET("/:id/simulate", simulateHandler.Simulate)

		protected.GET("/:id/settings", settingsHandler.SettingsPage)

		protected.POST("/:id/settings", settingsHandler.SaveSettings)

		protected.GET("/:id/test", testHandler.TestPage)

		protected.POST("/:id/testing/start", gameplayHandler.StartTesting)

		protected.GET("/:id/photos", photoHandler.PhotosPage)

		protected.POST("/:id/photos", photoHandler.UploadPhoto)

		protected.DELETE("/:id/photos/:photo_id", photoHandler.DeletePhoto)

		// ============================================================
		// ОТЗЫВЫ
		// ============================================================

		protected.GET("/:id/review", reviewHandler.ShowForm)

		// M5 (pass 36) + pass 40: rate limit на создание отзывов — раньше
		// эндпоинт можно было флудить (пачки отзывов).
		protected.POST("/:id/review", middleware.CodeSubmissionRateLimit(1*time.Minute, 10), reviewHandler.Create)
	}

	// API для autocomplete поиска игр
	// #9: публичный эндпоинт — dedicated per-IP лимитер (enumeration/scraping).
	api := r.Group("/api/search", middleware.LoginRateLimit(time.Minute, 20))
	api.GET("/games", autocompleteHandler.Games)

	// API для статистики игры (AJAX)
	apiStats := r.Group("/api/games")
	apiStats.Use(middleware.OptionalAuth(authService))
	{
		apiStats.GET("/:id/stats", gameStatsHandler.Show)
	}
}

// RegisterGameplayRoutes регистрирует маршруты игрового процесса.
func RegisterGameplayRoutes(
	r *gin.RouterGroup,
	handler *GameplayHandler,
	coAuthorSvc *CoAuthorService,
	sseMgr *SSEManager,
	gameRepo GameRepository,
	passingRepo GamePassingRepository,
) {
	r.GET("/game/:passing_id", handler.ShowGame)

	r.POST("/game/:passing_id/submit", middleware.CodeSubmissionRateLimit(1*time.Minute, 10), handler.SubmitCode)

	r.POST("/game/:passing_id/hint", middleware.CodeSubmissionRateLimit(1*time.Minute, 10), handler.UseHint)

	r.POST("/game/:passing_id/file", middleware.CodeSubmissionRateLimit(1*time.Minute, 10), handler.SubmitFile)

	r.POST("/game/:passing_id/accept", middleware.CodeSubmissionRateLimit(1*time.Minute, 20), handler.AcceptAnswer)

	// ============================================================
	// ТЕСТОВЫЕ МАРШРУТЫ
	// ============================================================

	r.GET("/testing/:passing_id", handler.ShowTestGame)

	r.POST("/testing/:passing_id/submit", middleware.CodeSubmissionRateLimit(1*time.Minute, 10), handler.SubmitTestCode)

	r.POST("/testing/:passing_id", middleware.CodeSubmissionRateLimit(1*time.Minute, 10), handler.SubmitTestCode)

	r.POST("/testing/:passing_id/skip", handler.SkipTestLevel)

	r.GET("/game/:passing_id/sse", middleware.SSERateLimit(1*time.Minute, 10), SSEHandler(sseMgr, gameRepo, passingRepo, coAuthorSvc))
	r.GET("/game/sse/:game_id", middleware.SSERateLimit(1*time.Minute, 10), SSEGameHandler(sseMgr, gameRepo, passingRepo, coAuthorSvc))
}
