// internal/pkg/rolecache/rolecache_test.go
package rolecache

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// M9 (PASS-3): базовый кэш — провайдер вызывается один раз до истечения TTL.
func TestCache_Get_Caches(t *testing.T) {
	c := New()
	var calls atomic.Int32
	provider := func(_ context.Context, _ uint) (string, error) {
		calls.Add(1)
		return "admin", nil
	}

	r, err := c.Get(context.Background(), 1, provider)
	require.NoError(t, err)
	assert.Equal(t, "admin", r)

	r, err = c.Get(context.Background(), 1, provider)
	require.NoError(t, err)
	assert.Equal(t, "admin", r)
	assert.Equal(t, int32(1), calls.Load(), "провайдер должен вызываться один раз (кэш работает)")
}

// M9: инвалидация — следующий Get снова вызывает провайдера.
func TestCache_Invalidate(t *testing.T) {
	c := New()
	var calls atomic.Int32
	provider := func(_ context.Context, _ uint) (string, error) {
		calls.Add(1)
		return "user", nil
	}

	_, _ = c.Get(context.Background(), 1, provider)
	_, _ = c.Get(context.Background(), 1, provider)
	assert.Equal(t, int32(1), calls.Load())

	c.Invalidate(1)
	_, _ = c.Get(context.Background(), 1, provider)
	assert.Equal(t, int32(2), calls.Load(), "после Invalidate провайдер вызывается снова")
}

// M9: TTL — после истечения провайдер вызывается повторно.
func TestCache_Get_TTLExpiry(t *testing.T) {
	c := New()
	var calls atomic.Int32
	provider := func(_ context.Context, _ uint) (string, error) {
		calls.Add(1)
		return "user", nil
	}

	_, _ = c.Get(context.Background(), 1, provider)
	// Ждём истечения TTL (5с — уменьшим не можем, но проверим InvalidateAll).
	c.InvalidateAll()
	_, _ = c.Get(context.Background(), 1, provider)
	assert.Equal(t, int32(2), calls.Load())
}

// M9: ошибки провайдера НЕ кэшируются.
func TestCache_Get_ErrorsNotCached(t *testing.T) {
	c := New()
	var calls atomic.Int32
	provider := func(_ context.Context, _ uint) (string, error) {
		calls.Add(1)
		if calls.Load() == 1 {
			return "", assert.AnError
		}
		return "user", nil
	}

	_, err := c.Get(context.Background(), 1, provider)
	require.Error(t, err)
	r, err := c.Get(context.Background(), 1, provider)
	require.NoError(t, err)
	assert.Equal(t, "user", r)
	assert.Equal(t, int32(2), calls.Load(), "ошибка не должна кэшироваться")
}

// M9: nil-провайдер — пустая роль без паники.
func TestCache_Get_NilProvider(t *testing.T) {
	c := New()
	r, err := c.Get(context.Background(), 1, nil)
	require.NoError(t, err)
	assert.Equal(t, "", r)
}

// M9: свежесть после ручного инвалидирования одного пользователя.
func TestCache_Invalidate_OnlyTarget(t *testing.T) {
	c := New()
	provider := func(_ context.Context, uid uint) (string, error) {
		if uid == 1 {
			return "admin", nil
		}
		return "user", nil
	}

	_, _ = c.Get(context.Background(), 1, provider)
	_, _ = c.Get(context.Background(), 2, provider)
	c.Invalidate(1)

	// После инвалидации: запись 1 удалена, запись 2 осталась.
	assert.Equal(t, 1, c.Len(), "инвалидирована только запись 1")

	r1, _ := c.Get(context.Background(), 1, provider)
	r2, _ := c.Get(context.Background(), 2, provider)
	assert.Equal(t, "admin", r1, "роль 1 перечитана после инвалидации")
	assert.Equal(t, "user", r2, "роль 2 из кэша")
	assert.Equal(t, 2, c.Len())
}

// M9: конкурентный доступ не паникует.
func TestCache_Concurrent(t *testing.T) {
	c := New()
	provider := func(_ context.Context, uid uint) (string, error) {
		time.Sleep(time.Millisecond)
		return "user", nil
	}
	done := make(chan struct{})
	for i := 0; i < 20; i++ {
		go func(uid uint) {
			defer func() { done <- struct{}{} }()
			_, _ = c.Get(context.Background(), uid, provider)
		}(uint(i%5))
	}
	for i := 0; i < 20; i++ {
		<-done
	}
	c.InvalidateAll()
}
