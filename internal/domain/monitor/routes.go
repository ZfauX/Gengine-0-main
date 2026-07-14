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
	userService *user.UserService,
	gameService *game.GameService,
) {
	chatRepo := NewGormChatRepo(db)
	blackboxRepo := NewGormBlackboxRepo(db)

	chatService := NewChatService(chatRepo)
	blackboxVoteService := NewBlackboxVoteService(blackboxRepo, gameRepo, cfg)

	monitorHandler := NewMonitorHandler(db, monitorSvc, blackboxVoteService, chatService, hub, userService, gameService)

	authRequired := middleware.AuthRequired(authService)
	gameManager := middleware.GameManager(coAuthorSvc)

	protected := router.Group("/")
	protected.Use(authRequired)

	gameGroup := protected.Group("/games/:id")
	gameGroup.Use(gameManager)
	{
		// @Summary Страница мониторинга игры
		// @Description Отображает страницу с live-обновлениями прогресса игры (WebSocket или SSE)
		// @Tags monitor
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 200 {string} html "Страница мониторинга"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав (только автор или соавтор)"
		// @Router /games/{id}/monitor [get]
		// @Security JWT
		gameGroup.GET("/monitor", monitorHandler.MonitorPage)

		// @Summary Поток мониторинга (SSE)
		// @Description Устанавливает Server-Sent Events соединение для получения обновлений прогресса игры.
		// @Tags monitor
		// @Produce text/event-stream
		// @Param id path int true "ID игры"
		// @Success 200 {string} string "SSE поток обновлений"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /games/{id}/monitor/stream [get]
		// @Security JWT
		gameGroup.GET("/monitor/stream", monitorHandler.MonitorStreamSSE)

		// @Summary Данные мониторинга (JSON)
		// @Description Возвращает текущий snapshot прогресса игры в формате JSON. Используется как fallback при недоступности WebSocket.
		// @Tags monitor
		// @Produce json
		// @Param id path int true "ID игры"
		// @Success 200 {object} map[string]interface{} "Snapshot прогресса команд"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
		// @Router /games/{id}/monitor/data [get]
		// @Security JWT
		gameGroup.GET("/monitor/data", monitorHandler.MonitorData)
	}

	// @Summary WebSocket мониторинга
	// @Description Устанавливает WebSocket-соединение для получения обновлений прогресса игры.
	// @Tags monitor
	// @Param id path int true "ID игры"
	// @Success 101 {string} string "Switching Protocols"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
	// @Failure 429 {object} map[string]interface{} "Слишком много активных соединений"
	// @Router /games/{id}/monitor/ws [get]
	// @Security JWT
	protected.GET("/games/:id/monitor/ws", gameManager, monitorHandler.MonitorWS)

	// @Summary Страница чата игры
	// @Description Отображает страницу чата для игры (общий и командный чаты)
	// @Tags monitor
	// @Produce html
	// @Param id path int true "ID игры"
	// @Success 200 {string} html "Страница чата"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Router /games/{id}/chat [get]
	// @Security JWT
	protected.GET("/games/:id/chat", monitorHandler.ChatPage)

	// @Summary WebSocket чата
	// @Description Устанавливает WebSocket-соединение для обмена сообщениями в чате.
	// @Tags monitor
	// @Param room query string true "ID комнаты чата"
	// @Success 101 {string} string "Switching Protocols"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 429 {object} map[string]interface{} "Слишком много активных соединений"
	// @Router /chat/ws [get]
	// @Security JWT
	protected.GET("/chat/ws", monitorHandler.ChatWS)

	// @Summary ID комнат чата
	// @Description Возвращает ID общей и командной комнат чата для игры (для инициализации WebSocket)
	// @Tags monitor
	// @Produce json
	// @Param id path int true "ID игры"
	// @Success 200 {object} map[string]interface{} "ID комнат чата (general_room_id, team_room_id)"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Router /games/{id}/chat-rooms [get]
	// @Security JWT
	protected.GET("/games/:id/chat-rooms", monitorHandler.ChatRoomIDs)

	// @Summary Логи игры
	// @Description Отображает страницу с историей событий игры (включая попытки ввода кодов, подсказки и т.д.)
	// @Tags monitor
	// @Produce html
	// @Param id path int true "ID игры"
	// @Success 200 {string} html "Страница логов"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
	// @Router /games/{id}/logs [get]
	// @Security JWT
	protected.GET("/games/:id/logs", monitorHandler.ListLogs)

	// @Summary WebSocket логов
	// @Description Устанавливает WebSocket-соединение для потоковой передачи логов игры.
	// @Tags monitor
	// @Param id path int true "ID игры"
	// @Success 101 {string} string "Switching Protocols"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
	// @Failure 429 {object} map[string]interface{} "Слишком много активных соединений"
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
	// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 403 {object} map[string]interface{} "Недостаточно прав (только автор)"
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
	// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
	// @Router /voting/vote [post]
	// @Security JWT
	protected.POST("/voting/vote", monitorHandler.Vote)

	// @Summary Результаты голосования
	// @Description Возвращает текущие результаты голосования по сессии (количество голосов за каждый вариант)
	// @Tags monitor
	// @Produce json
	// @Param session_id path int true "ID сессии голосования"
	// @Success 200 {object} map[string]interface{} "Результаты голосования"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 500 {object} map[string]interface{} "Внутренняя ошибка"
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
	// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 403 {object} map[string]interface{} "Недостаточно прав (только автор)"
	// @Router /voting/{session_id}/close [post]
	// @Security JWT
	protected.POST("/voting/:session_id/close", gameManager, monitorHandler.CloseVoting)
}
