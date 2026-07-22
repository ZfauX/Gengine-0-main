// internal/pkg/events/handlers.go
package events

import (
	"context"
	"sync"
	"time"

	"gengine-0/internal/pkg/logging"
)

// GameFinishedHandler — обработчик завершения игры.
// Вызывается после транзакции, когда игра завершена.
type GameFinishedHandler struct {
	mu       sync.Mutex
	lastTime time.Time
	lastErr  error
}

// NewGameFinishedHandler создаёт новый handler.
func NewGameFinishedHandler() *GameFinishedHandler {
	return &GameFinishedHandler{
		lastTime: time.Time{},
	}
}

// Handle обрабатывает событие GameFinished.
func (h *GameFinishedHandler) Handle(event Event) {
	gameID, ok := event.Data["game_id"].(uint)
	if !ok {
		return
	}

	// Логирование
	logging.Info(event.Ctx).Uint("game_id", gameID).
		Str("event", string(GameFinished)).
		Msg("Игра завершена")

	// Здесь можно вызвать CalculateResults через MonitorService
	// Но лучше использовать async подход с queue
	go h.processGameFinished(event.Ctx, gameID)
}

func (h *GameFinishedHandler) processGameFinished(ctx context.Context, gameID uint) {
	h.mu.Lock()
	h.lastTime = time.Now()
	h.mu.Unlock()

	// Placeholder — реальный вызов будет через dependency injection
	// if monitorSvc != nil {
	//     _ = monitorSvc.CalculateResults(ctx, gameID)
	// }
	logging.Info(ctx).Uint("game_id", gameID).
		Str("event", string(GameFinished)).
		Msg("Обработка завершённы игры запущена")
}

// GetLastResult возвращает время последней обработки и ошибку (для тестов).
func (h *GameFinishedHandler) GetLastResult() (time.Time, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastTime, h.lastErr
}

// PassingStartedHandler — обработчик начала прохождения.
type PassingStartedHandler struct{}

// NewPassingStartedHandler создаёт handler.
func NewPassingStartedHandler() *PassingStartedHandler {
	return &PassingStartedHandler{}
}

// Handle обрабатывает событие PassingStarted.
func (h *PassingStartedHandler) Handle(event Event) {
	gameID, ok := event.Data["game_id"].(uint)
	if !ok {
		logging.Error(event.Ctx).
			Str("event", string(PassingStarted)).
			Msg("failed to assert game_id from event data")
		return
	}
	passingID, ok := event.Data["passing_id"].(uint)
	if !ok {
		logging.Error(event.Ctx).Uint("game_id", gameID).
			Str("event", string(PassingStarted)).
			Msg("failed to assert passing_id from event data")
		return
	}

	logging.Info(event.Ctx).
		Uint("game_id", gameID).
		Uint("passing_id", passingID).
		Str("event", string(PassingStarted)).
		Msg("Прохождение запущено")
}

// UserRegisteredHandler — обработчик регистрации пользователя.
type UserRegisteredHandler struct{}

// NewUserRegisteredHandler создаёт handler.
func NewUserRegisteredHandler() *UserRegisteredHandler {
	return &UserRegisteredHandler{}
}

// Handle обрабатывает событие UserRegistered.
func (h *UserRegisteredHandler) Handle(event Event) {
	userID, ok := event.Data["user_id"].(uint)
	if !ok {
		logging.Error(event.Ctx).
			Str("event", string(UserRegistered)).
			Msg("failed to assert user_id from event data")
		return
	}
	email, ok := event.Data["email"].(string)
	if !ok {
		logging.Error(event.Ctx).Uint("user_id", userID).
			Str("event", string(UserRegistered)).
			Msg("failed to assert email from event data")
		return
	}

	logging.Info(event.Ctx).
		Uint("user_id", userID).
		Str("email", email).
		Str("event", string(UserRegistered)).
		Msg("Пользователь зарегистрирован")
}
