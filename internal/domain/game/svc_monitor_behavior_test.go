// internal/domain/game/svc_monitor_behavior_test.go
package game_test

import (
	"testing"
	"time"

	"gengine-0/internal/domain/game"

	"github.com/stretchr/testify/assert"
)

// TestCheckSuspiciousAttempts_HighFrequency проверяет срабатывание детекции
// при высокой частоте попыток (50+ попыток за 5-минутное окно = 10/мин).
func TestCheckSuspiciousAttempts_HighFrequency(t *testing.T) {
	var attempts []game.AttemptRecord
	base := time.Now().Add(-5 * time.Minute)
	for i := 0; i < 51; i++ {
		attempts = append(attempts, game.AttemptRecord{
			PassingID: 1,
			Code:      "code",
			Success:   false,
			CreatedAt: base.Add(time.Duration(i) * time.Second),
		})
	}
	reason := game.CheckSuspiciousAttempts(attempts)
	assert.Contains(t, reason, "Подозрительная частота")
}

// TestCheckSuspiciousAttempts_BruteForceStreak проверяет детекцию брутфорса:
// 3+ одинаковых неверных кода подряд.
func TestCheckSuspiciousAttempts_BruteForceStreak(t *testing.T) {
	base := time.Now()
	attempts := []game.AttemptRecord{
		{PassingID: 1, Code: "1111", Success: false, CreatedAt: base},
		{PassingID: 1, Code: "2222", Success: false, CreatedAt: base.Add(time.Second)},
		{PassingID: 1, Code: "2222", Success: false, CreatedAt: base.Add(2 * time.Second)},
		{PassingID: 1, Code: "2222", Success: false, CreatedAt: base.Add(3 * time.Second)},
	}
	reason := game.CheckSuspiciousAttempts(attempts)
	assert.Contains(t, reason, "Брутфорс")
}

// TestCheckSuspiciousAttempts_ResetOnSuccess проверяет, что успешная попытка
// сбрасывает серию одинаковых неверных кодов.
func TestCheckSuspiciousAttempts_ResetOnSuccess(t *testing.T) {
	base := time.Now()
	attempts := []game.AttemptRecord{
		{PassingID: 1, Code: "1111", Success: false, CreatedAt: base},
		{PassingID: 1, Code: "1111", Success: false, CreatedAt: base.Add(time.Second)},
		{PassingID: 1, Code: "1111", Success: true, CreatedAt: base.Add(2 * time.Second)},
		{PassingID: 1, Code: "1111", Success: false, CreatedAt: base.Add(3 * time.Second)},
	}
	reason := game.CheckSuspiciousAttempts(attempts)
	assert.Equal(t, "", reason)
}

// TestCheckSuspiciousAttempts_Clean проверяет, что нормальный паттерн попыток
// не помечается как подозрительный.
func TestCheckSuspiciousAttempts_Clean(t *testing.T) {
	base := time.Now()
	attempts := []game.AttemptRecord{
		{PassingID: 1, Code: "1111", Success: false, CreatedAt: base},
		{PassingID: 1, Code: "2222", Success: false, CreatedAt: base.Add(30 * time.Second)},
		{PassingID: 1, Code: "3333", Success: true, CreatedAt: base.Add(time.Minute)},
	}
	reason := game.CheckSuspiciousAttempts(attempts)
	assert.Equal(t, "", reason)
}
