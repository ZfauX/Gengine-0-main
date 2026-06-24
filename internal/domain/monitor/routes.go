// internal/domain/monitor/routes.go
package monitor

import (
	"gengine-0/internal/config"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterRoutes регистрирует маршруты мониторинга.
// Принимает готовый authService для избежания дублирования инициализации.
func RegisterRoutes(
	router *gin.Engine,
	db *gorm.DB,
	hub *ws.RoomHub,
	cfg *config.Config,
	coAuthorSvc *game.CoAuthorService,
	monitorSvc *game.MonitorService,
	attemptSvc *game.AttemptService,
	progressSvc *game.LevelProgressService,
	authService *user.AuthService, // добавлен параметр
) {
	chatService := NewChatService(db)
	blackboxVoteService := NewBlackboxVoteService(db, cfg)

	monitorHandler := NewMonitorHandler(db, monitorSvc, blackboxVoteService, chatService, hub)

	authRequired := middleware.AuthRequired(authService)
	gameManager := middleware.GameManager(coAuthorSvc)

	protected := router.Group("/")
	protected.Use(authRequired)

	gameGroup := protected.Group("/games/:id")
	gameGroup.Use(gameManager)
	{
		gameGroup.GET("/monitor", monitorHandler.MonitorPage)
		// disqualify удалён, так как MonitorHandler не содержит такого метода
	}

	protected.GET("/games/:id/monitor/ws", gameManager, monitorHandler.MonitorWS)

	protected.GET("/games/:id/chat", monitorHandler.ChatPage)
	protected.GET("/chat/ws", monitorHandler.ChatWS)
	protected.GET("/games/:id/chat-rooms", monitorHandler.ChatRoomIDs)

	protected.GET("/games/:id/logs", monitorHandler.ListLogs)
	protected.GET("/games/:id/logs/ws", monitorHandler.LogsWS)

	protected.POST("/voting/start", gameManager, monitorHandler.StartVoting)
	protected.POST("/voting/vote", monitorHandler.Vote)
	protected.GET("/voting/:session_id/results", monitorHandler.GetVotingResults)
	protected.POST("/voting/:session_id/close", gameManager, monitorHandler.CloseVoting)
}
