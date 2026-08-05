// internal/domain/game/svc_snapshot.go
package game

import (
	"sync"
	"time"
)

// SnapshotDispatcher дебаунсит пересчёт снапшота мониторинга (S3).
// Активная игра: множество попыток кода за короткий промежуток сливаются в
// один тяжёлый пересчёт (GetOrFetchSnapshot + CalculateResults + broadcast),
// который выполняется асинхронно — HTTP-запрос игрока не платит за него.
//
// Важно: если диспетчер не установлен в GamePlayService (тесты), используется
// синхронный fallback — тесты не ломаются от фоновых горутин с БД.
type SnapshotDispatcher struct {
	delay time.Duration
	fn    func(gameID uint)

	mu     sync.Mutex
	timers map[uint]*time.Timer
	closed bool
}

// NewSnapshotDispatcher создаёт дебаунс-диспетчер. fn вызывается асинхронно
// для каждой игры после паузы delay без новых событий.
func NewSnapshotDispatcher(delay time.Duration, fn func(gameID uint)) *SnapshotDispatcher {
	return &SnapshotDispatcher{
		delay:  delay,
		fn:     fn,
		timers: make(map[uint]*time.Timer),
	}
}

// Schedule откладывает пересчёт снапшота для игры. Повторный вызов сбрасывает
// таймер (debounce): за время delay накапливаются все события игры.
func (d *SnapshotDispatcher) Schedule(gameID uint) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	if t, ok := d.timers[gameID]; ok {
		t.Stop()
	}
	d.timers[gameID] = time.AfterFunc(d.delay, func() {
		d.flush(gameID)
	})
}

// flush удаляет таймер и вызывает рабочую функцию вне блокировки.
func (d *SnapshotDispatcher) flush(gameID uint) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	delete(d.timers, gameID)
	d.mu.Unlock()

	d.fn(gameID)
}

// Close останавливает диспетчер: новые Schedule игнорируются, ожидающие
// таймеры отменяются. Уже выполняющиеся fn завершаются самостоятельно
// (короткие операции; ошибки БД логируются внутри).
func (d *SnapshotDispatcher) Close() {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.closed = true
	for _, t := range d.timers {
		t.Stop()
	}
	d.timers = make(map[uint]*time.Timer)
	d.mu.Unlock()
}
