// internal/domain/payment/repository_integration_test.go
// L6 (PASS-7): атомарность MarkSucceededIfPending — только один переход.
//go:build integration

package payment

import (
	"context"
	"sync"
	"testing"

	"gengine-0/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// L6 (PASS-7): MarkSucceededIfPending возвращает true ровно один раз при
// параллельных вызовах (гонка двух webhook'ов) — второй вызов видит succeeded
// и не переходит снова. Раньше оба читали pending и слали дубликат уведомления.
func TestMarkSucceededIfPending_AtomicTransition(t *testing.T) {
	db := testutil.SetupPostgresDB(t, &Payment{})
	// SetupPostgresDB устанавливает search_path session-level (SET) — при
	// пуле соединений новые коннекты его не наследуют. Для теста атомарности
	// одно соединение достаточно (PostgreSQL сериализует UPDATE WHERE).
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	repo := NewGormPaymentRepo(db)
	ctx := context.Background()

	p := &Payment{UserID: 1, AmountKopecks: 1000, Currency: "RUB", Status: StatusPending}
	require.NoError(t, repo.Create(ctx, p))

	const workers = 8
	var wg sync.WaitGroup
	results := make([]bool, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ok, err := repo.MarkSucceededIfPending(ctx, p.ID)
			if err != nil {
				t.Errorf("MarkSucceededIfPending: %v", err)
				return
			}
			results[idx] = ok
		}(i)
	}
	wg.Wait()

	transitions := 0
	for _, ok := range results {
		if ok {
			transitions++
		}
	}
	assert.Equal(t, 1, transitions, "ровно один вызов должен совершить переход")

	// Повторный вызов на уже succeeded — false.
	ok, err := repo.MarkSucceededIfPending(ctx, p.ID)
	require.NoError(t, err)
	assert.False(t, ok)

	got, err := repo.GetByID(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusSucceeded, got.Status)
}
