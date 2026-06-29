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
// @tags monitor
func RegisterRoutes(
	router *gin.Engine,
	db *gorm.DB,
	hub *ws.RoomHub,
	cfg *config.Config,
	coAuthorSvc *game.CoAuthorService,
	monitorSvc *game.MonitorService,
	attemptSvc *game.AttemptService,
	progressSvc *game.LevelProgressService,
	authService *user.AuthService,
	gameRepo game.GameRepository,
) {
	chatRepo := NewGormChatRepo(db)
	blackboxRepo := NewGormBlackboxRepo(db)

	chatService := NewChatService(chatRepo)
	blackboxVoteService := NewBlackboxVoteService(blackboxRepo, gameRepo, cfg)

	monitorHandler := NewMonitorHandler(db, monitorSvc, blackboxVoteService, chatService, hub)

	authRequired := middleware.AuthRequired(authService)
	gameManager := middleware.GameManager(coAuthorSvc)

	protected := router.Group("/")
	protected.Use(authRequired)

	gameGroup := protected.Group("/games/:id")
	gameGroup.Use(gameManager)
	{
		// @Summary Страница мониторинга игры
		// @Description Отображает страницу с live-обновлениями прогресса игры
		// @Tags monitor
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 200 {string} html "Страница мониторинга"
		// @Router /games/{id}/monitor [get]
		// @Security JWT
		gameGroup.GET("/monitor", monitorHandler.MonitorPage)

		// @Summary Поток мониторинга (SSE)
		// @Description Устанавливает Server-Sent Events соединение для получения обновлений прогресса игры.
		// @Description Это лёгкая альтернатива WebSocket для однонаправленного мониторинга, снижающая нагрузку на сервер.
		// @Tags monitor
		// @Produce text/event-stream
		// @Param id path int true "ID игры"
		// @Success 200 {string} string "SSE поток обновлений"
		// @Router /games/{id}/monitor/stream [get]
		// @Security JWT
		gameGroup.GET("/monitor/stream", monitorHandler.MonitorStreamSSE)
	}

	// @Summary WebSocket мониторинга
	// @Description Устанавливает WebSocket-соединение для получения обновлений прогресса игры.
	// @Description Рекомендуется использовать SSE вместо WebSocket для мониторинга, так как SSE легче и не требует поддержания двустороннего канала.
	// @Tags monitor
	// @Param id path int true "ID игры"
	// @Success 101 {string} string "Switching Protocols"
	// @Router /games/{id}/monitor/ws [get]
	// @Security JWT
	protected.GET("/games/:id/monitor/ws", gameManager, monitorHandler.MonitorWS)

	// @Summary Страница чата игры
	// @Description Отображает страницу чата для игры (общий и командный)
	// @Tags monitor
	// @Produce html
	// @Param id path int true "ID игры"
	// @Success 200 {string} html "Страница чата"
	// @Router /games/{id}/chat [get]
	// @Security JWT
	protected.GET("/games/:id/chat", monitorHandler.ChatPage)

	// @Summary WebSocket чата
	// @Description Устанавливает WebSocket-соединение для обмена сообщениями в чате.
	// @Description Для чата WebSocket предпочтительнее, так как требуется двусторонняя связь в реальном времени.
	// @Tags monitor
	// @Param room query string true "ID комнаты чата"
	// @Success 101 {string} string "Switching Protocols"
	// @Router /chat/ws [get]
	// @Security JWT
	protected.GET("/chat/ws", monitorHandler.ChatWS)

	// @Summary ID комнат чата
	// @Description Возвращает ID общей и командной комнат чата для игры
	// @Tags monitor
	// @Produce json
	// @Param id path int true "ID игры"
	// @Success 200 {object} map[string]interface{} "ID комнат чата"
	// @Router /games/{id}/chat-rooms [get]
	// @Security JWT
	protected.GET("/games/:id/chat-rooms", monitorHandler.ChatRoomIDs)

	// @Summary Логи игры
	// @Description Отображает страницу с историей событий игры
	// @Tags monitor
	// @Produce html
	// @Param id path int true "ID игры"
	// @Success 200 {string} html "Страница логов"
	// @Router /games/{id}/logs [get]
	// @Security JWT
	protected.GET("/games/:id/logs", monitorHandler.ListLogs)

	// @Summary WebSocket логов
	// @Description Устанавливает WebSocket-соединение для потоковой передачи логов игры.
	// @Description Для логов также рекомендуется использовать SSE, если не требуется отправка команд с клиента.
	// @Tags monitor
	// @Param id path int true "ID игры"
	// @Success 101 {string} string "Switching Protocols"
	// @Router /games/{id}/logs/ws [get]
	// @Security JWT
	protected.GET("/games/:id/logs/ws", monitorHandler.LogsWS)

	// @Summary Запуск голосования
	// @Description Запускает голосование на уровне-чёрном ящике (доступно автору игры)
	// @Tags monitor
	// @Accept x-www-form-urlencoded
	// @Produce json
	// @Param passing_id formData uint true "ID прохождения"
	// @Param level_id formData uint true "ID уровня"
	// @Success 200 {object} map[string]interface{} "Голосование запущено"
	// @Failure 400 {object} map[string]interface{} "Ошибка"
	// @Router /voting/start [post]
	// @Security JWT
	protected.POST("/voting/start", gameManager, monitorHandler.StartVoting)

	// @Summary Голосование
	// @Description Команда голосует за вариант ответа на уровне-чёрном ящике
	// @Tags monitor
	// @Accept x-www-form-urlencoded
	// @Produce json
	// @Param session_id formData uint true "ID сессии голосования"
	// @Param team_id formData uint true "ID команды"
	// @Param option formData string true "Выбранный вариант"
	// @Success 200 {object} map[string]interface{} "Голос учтён"
	// @Failure 400 {object} map[string]interface{} "Ошибка"
	// @Router /voting/vote [post]
	// @Security JWT
	protected.POST("/voting/vote", monitorHandler.Vote)

	// @Summary Результаты голосования
	// @Description Возвращает текущие результаты голосования по сессии
	// @Tags monitor
	// @Produce json
	// @Param session_id path int true "ID сессии голосования"
	// @Success 200 {object} map[string]interface{} "Результаты голосования"
	// @Failure 500 {object} map[string]interface{} "Ошибка"
	// @Router /voting/{session_id}/results [get]
	// @Security JWT
	protected.GET("/voting/:session_id/results", monitorHandler.GetVotingResults)

	// @Summary Закрытие голосования
	// @Description Завершает голосование и определяет победителя (доступно автору игры)
	// @Tags monitor
	// @Accept x-www-form-urlencoded
	// @Produce json
	// @Param session_id path int true "ID сессии голосования"
	// @Success 200 {object} map[string]interface{} "Победивший вариант"
	// @Failure 400 {object} map[string]interface{} "Ошибка"
	// @Router /voting/{session_id}/close [post]
	// @Security JWT
	protected.POST("/voting/:session_id/close", gameManager, monitorHandler.CloseVoting)
}
