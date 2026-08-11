// internal/pkg/cache/valkey_test.go
package cache

import (
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// DEEP-REVIEW (pass 46): при недоступном Valkey NewValkeyClient должен закрывать
// клиент, а не оставлять фоновые goroutine go-redis (tryDial/CircuitBreaker),
// которые вечно ретраят подключение.
func TestNewValkeyClient_UnavailableClosesClient(t *testing.T) {
	before := runtime.NumGoroutine()

	// Недоступный адрес — Ping провалится, клиент должен закрыться.
	client := NewValkeyClient("127.0.0.1", "1", "", 10, 0, 2)
	assert.Nil(t, client, "клиент должен быть nil при недоступном Valkey")

	// Даём фоновым goroutine время завершиться после Close.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if runtime.NumGoroutine() <= before+2 { // +2 на допуск (тестовые runner)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	after := runtime.NumGoroutine()
	assert.LessOrEqual(t, after, before+2,
		"после неудачного подключения не должно остаться висящих goroutine (было %d, стало %d)", before, after)
}
