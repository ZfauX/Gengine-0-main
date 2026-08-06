// internal/domain/game/svc_snapshot_test.go
package game_test

import (
	"sync/atomic"
	"testing"
	"time"

	"gengine-0/internal/domain/game"

	"github.com/stretchr/testify/assert"
)

// TestSnapshotDispatcher_Debounce: серия Schedule для одной игры вызывает fn
// один раз после паузы (debounce).
func TestSnapshotDispatcher_Debounce(t *testing.T) {
	var calls int32
	d := game.NewSnapshotDispatcher(50*time.Millisecond, func(uint) {
		atomic.AddInt32(&calls, 1)
	})

	for i := 0; i < 10; i++ {
		d.Schedule(42)
		time.Sleep(10 * time.Millisecond)
	}

	time.Sleep(150 * time.Millisecond)
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls), "debounce должен слить все события в один вызов")
}

// TestSnapshotDispatcher_SeparateGames: разные игры не дебаунсятся вместе.
func TestSnapshotDispatcher_SeparateGames(t *testing.T) {
	var calls int32
	d := game.NewSnapshotDispatcher(30*time.Millisecond, func(uint) {
		atomic.AddInt32(&calls, 1)
	})

	d.Schedule(1)
	d.Schedule(2)
	d.Schedule(3)

	time.Sleep(150 * time.Millisecond)
	assert.Equal(t, int32(3), atomic.LoadInt32(&calls))
}

// TestSnapshotDispatcher_Close: после Close новые Schedule игнорируются,
// ожидающие таймеры отменяются.
func TestSnapshotDispatcher_Close(t *testing.T) {
	var calls int32
	d := game.NewSnapshotDispatcher(30*time.Millisecond, func(uint) {
		atomic.AddInt32(&calls, 1)
	})

	d.Schedule(7)
	d.Close()
	d.Schedule(7)

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(0), atomic.LoadInt32(&calls), "Close должен отменить ожидающие таймеры и запретить новые")
}
