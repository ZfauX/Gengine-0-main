// internal/domain/monitor/routes.go
package monitor

import (
	"time"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes регистрирует маршруты мониторинга.
// @tags monitor
func RegisterRoutes(
	router *gin.RouterGroup,
	chatService *ChatService,
	blackboxVoteService *BlackboxVoteService,
	hub *ws.RoomHub,
	coAuthorSvc *game.CoAuthorService,
	monitorSvc *game.MonitorService,
	authService *user.AuthService,
	userService *user.UserService,
	gameService *game.GameService,
) {
	monitorHandler := NewMonitorHandler(monitorSvc, blackboxVoteService, chatService, hub, userService, gameService, coAuthorSvc)
	// IDEA-6: онлайн-индикатор в чате — presence-рассылка по WS.
	monitorHandler.setupChatPresence()

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

	// B-6 (pass 45): общий чат сервера.
	protected.GET("/chat/global", monitorHandler.GlobalChatPage)
	protected.GET("/chat/global/room", monitorHandler.GlobalChatRoomID)

	// B-4 (pass 45): комнаты игры — список и создание (менеджер).
	// DEEP-REVIEW (pass 46): GameRooms не проверял права — любой пользователь
	// мог перечислить комнаты любой игры (IDOR). Добавлен gameManager.
	protected.GET("/games/:id/chat/rooms", gameManager, monitorHandler.GameRooms)
	// L7 (PASS-6): rate-limit на создание комнат — менеджер не плодит
	// неограниченное число комнат (спам/мусор).
	protected.POST("/games/:id/chat/rooms", gameManager, middleware.CreateRoomRateLimit(1*time.Minute, 20), monitorHandler.CreateRoom)

	// B-7 (pass 45): личный чат 1-на-1.
	// M5 (PASS-4): rate-limit — раньше любой аутентифицированный создавал
	// комнаты с любым user_id без ограничений (спам/PII-перечисление).
	protected.GET("/chat/personal/:user_id", middleware.PersonalChatRateLimit(1*time.Minute, 30), monitorHandler.PersonalChat)

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
	protected.GET("/games/:id/logs", gameManager, monitorHandler.ListLogs)

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
	protected.GET("/games/:id/logs/ws", gameManager, monitorHandler.LogsWS)

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
	// S-44-4 (pass 44): rate-limit как на vote — start/close выполняют DB-работу
	// и рассылают письма капитанам при SMTP; спам менеджером ограничен.
	protected.POST("/voting/start", middleware.CodeSubmissionRateLimit(1*time.Minute, 20), monitorHandler.StartVoting)

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
	protected.POST("/voting/vote", middleware.CodeSubmissionRateLimit(1*time.Minute, 20), monitorHandler.Vote)

	// @Summary Результаты голосования
	// @Description Возвращает текущие результаты голосования по сессии (количество голосов за каждый вариант)
	// @Tags monitor
	// @Produce json
	// @Param session_id path int true "ID сессии голосования"
	// @Success 200 {object} map[string]interface{} "Результаты голосования"
	// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
	// @Failure 500 {object} map[string]interface{} "handler.internal_error"
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
	// S-44-4 (pass 44): rate-limit на close — пересчёт результатов + письма капитанам.
	protected.POST("/voting/:session_id/close", middleware.CodeSubmissionRateLimit(1*time.Minute, 20), monitorHandler.CloseVoting)
}
