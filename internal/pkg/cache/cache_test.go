package cache

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCache(t *testing.T) {
	c, err := NewCache(time.Minute, 10*time.Minute)
	require.NoError(t, err)
	assert.NotNil(t, c)
}

func TestNewCacheWithLRU(t *testing.T) {
	c := NewCacheWithLRU(time.Minute, 10*time.Minute, 100)
	assert.NotNil(t, c)
}

func TestCache_SetAndDelete(t *testing.T) {
	c, err := NewCache(time.Minute, 10*time.Minute)
	require.NoError(t, err)
	defer c.Close()

	c.Set("key1", "value1", time.Minute)
	val, ok := c.Get("key1")
	assert.True(t, ok)
	assert.Equal(t, "value1", val)
}

func TestCache_Get_Missing(t *testing.T) {
	c, _ := NewCache(time.Minute, 10*time.Minute)
	defer c.Close()

	val, ok := c.Get("nonexistent")
	assert.False(t, ok)
	assert.Nil(t, val)
}

func TestCache_Get_Expired(t *testing.T) {
	c, _ := NewCache(time.Minute, 10*time.Minute)
	defer c.Close()

	c.Set("key1", "value1", -time.Second)
	// Ждём обработки
	assert.Eventually(t, func() bool {
		_, ok := c.Get("key1")
		return !ok
	}, 500*time.Millisecond, 50*time.Millisecond)
}

func TestCache_Get_Expired_RemovesFromLRU(t *testing.T) {
	c := NewCacheWithLRU(time.Minute, 10*time.Minute, 10)
	defer c.Close()

	c.Set("key1", "value1", -time.Second)
	c.Get("key1")
	// Ждём удаления
	assert.Eventually(t, func() bool {
		return c.lru.Len() == 0
	}, 500*time.Millisecond, 50*time.Millisecond)
}

func TestCache_SetDefault(t *testing.T) {
	c, _ := NewCache(time.Minute, 10*time.Minute)
	defer c.Close()

	c.SetDefault("key1", "value1")
	val, ok := c.Get("key1")
	assert.True(t, ok)
	assert.Equal(t, "value1", val)
}

func TestCache_Delete(t *testing.T) {
	c, _ := NewCache(time.Minute, 10*time.Minute)
	defer c.Close()

	c.Set("key1", "value1", time.Minute)
	c.Delete("key1")

	val, ok := c.Get("key1")
	assert.False(t, ok)
	assert.Nil(t, val)
}

func TestCache_DeleteByPrefix(t *testing.T) {
	c, _ := NewCache(time.Minute, 10*time.Minute)
	defer c.Close()

	c.Set("user:1:name", "Alice", time.Minute)
	c.Set("user:1:email", "alice@test.com", time.Minute)
	c.Set("game:1:title", "Test", time.Minute)

	c.DeleteByPrefix("user:1")

	_, ok := c.Get("user:1:name")
	assert.False(t, ok)
	_, ok = c.Get("game:1:title")
	assert.True(t, ok)
}

func TestCache_Flush(t *testing.T) {
	c, _ := NewCache(time.Minute, 10*time.Minute)
	defer c.Close()

	c.Set("key1", "value1", time.Minute)
	c.Set("key2", "value2", time.Minute)

	c.Flush()

	_, ok := c.Get("key1")
	assert.False(t, ok)
	_, ok = c.Get("key2")
	assert.False(t, ok)
}

func TestCache_GetOrSet(t *testing.T) {
	c, _ := NewCache(time.Minute, 10*time.Minute)
	defer c.Close()

	var callCount int
	fn := func() (any, error) {
		callCount++
		return "computed", nil
	}

	val, err := c.GetOrSet("key1", time.Minute, fn)
	require.NoError(t, err)
	assert.Equal(t, "computed", val)
	assert.Equal(t, 1, callCount)

	// Второй вызов должен вернуть кэшированное значение
	val, err = c.GetOrSet("key1", time.Minute, fn)
	require.NoError(t, err)
	assert.Equal(t, "computed", val)
	assert.Equal(t, 1, callCount) // функция не должна вызываться повторно
}

func TestCache_GetOrSetError(t *testing.T) {
	c, _ := NewCache(time.Minute, 10*time.Minute)
	defer c.Close()

	fn := func() (any, error) {
		return nil, errors.New("compute error")
	}

	_, err := c.GetOrSet("key1", time.Minute, fn)
	assert.Error(t, err)
}

func TestCache_ExtendTTL(t *testing.T) {
	c := NewCacheWithLRU(time.Minute, 10*time.Minute, 100)
	defer c.Close()

	// Тест 1: Продление существующего ключа
	c.Set("key1", "value1", 200*time.Millisecond)
	extended := c.ExtendTTL("key1", time.Minute)
	assert.True(t, extended)

	// Ждём, что ключ всё ещё существует (TTL продлён)
	time.Sleep(150 * time.Millisecond)
	val, ok := c.Get("key1")
	assert.True(t, ok)
	assert.Equal(t, "value1", val)

	// Тест 2: Продление несуществующего ключа
	extended = c.ExtendTTL("nonexistent", time.Minute)
	assert.False(t, extended)
}

func TestCache_ConcurrentAccess(t *testing.T) {
	c, _ := NewCache(time.Minute, 10*time.Minute)
	defer c.Close()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(3)
		go func(n int) {
			defer wg.Done()
			c.Set("key", n, time.Minute)
		}(i)
		go func() {
			defer wg.Done()
			c.Get("key")
		}()
		go func() {
			defer wg.Done()
			c.Delete("key")
		}()
	}
	wg.Wait()
}

func TestCache_ConcurrentPrefixDelete(t *testing.T) {
	c, _ := NewCache(time.Minute, 10*time.Minute)
	defer c.Close()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			c.Set("prefix:key", n, time.Minute)
		}(i)
		go func() {
			defer wg.Done()
			c.DeleteByPrefix("prefix")
		}()
	}
	wg.Wait()
}

func TestCache_GetOrSetString(t *testing.T) {
	c, _ := NewCache(time.Minute, 10*time.Minute)
	defer c.Close()

	val, err := c.GetOrSetString("key1", time.Minute, func() (string, error) {
		return "string_value", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "string_value", val)
}

func TestCache_GetOrSetInt(t *testing.T) {
	c, _ := NewCache(time.Minute, 10*time.Minute)
	defer c.Close()

	val, err := c.GetOrSetInt("key1", time.Minute, func() (int, error) {
		return 42, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 42, val)
}

func TestCache_GetOrSetFloat64(t *testing.T) {
	c, _ := NewCache(time.Minute, 10*time.Minute)
	defer c.Close()

	val, err := c.GetOrSetFloat64("key1", time.Minute, func() (float64, error) {
		return 3.14, nil
	})
	require.NoError(t, err)
	assert.InDelta(t, 3.14, val, 0.01)
}

func TestCache_GetOrSetStringWithTTL(t *testing.T) {
	c, _ := NewCache(time.Minute, 10*time.Minute)
	defer c.Close()

	callCount := 0
	fn := func() (string, error) {
		callCount++
		return "value", nil
	}

	val, err := c.GetOrSetStringWithTTL("key1", time.Minute, fn)
	require.NoError(t, err)
	assert.Equal(t, "value", val)
	assert.Equal(t, 1, callCount)

	// Второй вызов должен вернуть кэшированное значение
	val, err = c.GetOrSetStringWithTTL("key1", time.Minute, fn)
	require.NoError(t, err)
	assert.Equal(t, "value", val)
	assert.Equal(t, 1, callCount)
}

func TestNoopCache(t *testing.T) {
	c := NewNoopCache()
	val, ok := c.Get("key")
	assert.False(t, ok)
	assert.Nil(t, val)

	c.Set("key", "value", time.Minute)
	c.SetDefault("key", "value")
	c.Delete("key")
	c.DeleteByPrefix("prefix")
	c.Flush()

	val, err := c.GetOrSet("key", time.Minute, func() (any, error) {
		return "noop", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "noop", val)

	str, err := c.GetOrSetString("key", time.Minute, func() (string, error) {
		return "noop", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "noop", str)

	str, err = c.GetOrSetStringWithTTL("key", time.Minute, func() (string, error) {
		return "noop", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "noop", str)

	assert.False(t, c.ExtendTTL("key", time.Minute))

	i, err := c.GetOrSetInt("key", time.Minute, func() (int, error) {
		return 0, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 0, i)

	f, err := c.GetOrSetFloat64("key", time.Minute, func() (float64, error) {
		return 0, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 0.0, f)
}

func TestCache_Stats(t *testing.T) {
	c := NewCacheWithLRU(time.Minute, 10*time.Minute, 100)
	defer c.Close()

	c.Set("key1", "value1", time.Minute)
	c.Set("key2", "value2", time.Minute)

	stats := c.Stats()
	assert.Equal(t, 2, stats["items"])
	assert.Equal(t, 100, stats["max_size"])
	assert.Equal(t, 0.02, stats["utilization"])
}

func TestCache_ExtendTTL_NotFound(t *testing.T) {
	c := NewCacheWithLRU(time.Minute, 10*time.Minute, 100)
	defer c.Close()

	extended := c.ExtendTTL("nonexistent", time.Minute)
	assert.False(t, extended)
}

func TestCache_DeleteByPrefix_Empty(t *testing.T) {
	c, _ := NewCache(time.Minute, 10*time.Minute)
	defer c.Close()

	// Не должно паниковать
	c.DeleteByPrefix("nonexistent")
}

func TestCache_GetWithCtx(t *testing.T) {
	c, _ := NewCache(time.Minute, 10*time.Minute)
	defer c.Close()

	c.Set("key1", "value1", time.Minute)
	val, ok := c.GetWithCtx(context.Background(), "key1")
	assert.True(t, ok)
	assert.Equal(t, "value1", val)
}

func TestCache_SetWithCtx(t *testing.T) {
	c, _ := NewCache(time.Minute, 10*time.Minute)
	defer c.Close()

	c.SetWithCtx(context.Background(), "key1", "value1", time.Minute)
	val, ok := c.Get("key1")
	assert.True(t, ok)
	assert.Equal(t, "value1", val)
}

func TestCache_DeleteWithCtx(t *testing.T) {
	c, _ := NewCache(time.Minute, 10*time.Minute)
	defer c.Close()

	c.Set("key1", "value1", time.Minute)
	c.DeleteWithCtx(context.Background(), "key1")
	_, ok := c.Get("key1")
	assert.False(t, ok)
}

func TestCache_DeleteByPrefixWithCtx(t *testing.T) {
	c, _ := NewCache(time.Minute, 10*time.Minute)
	defer c.Close()

	c.Set("prefix:key1", "value1", time.Minute)
	c.Set("prefix:key2", "value2", time.Minute)
	c.DeleteByPrefixWithCtx(context.Background(), "prefix")

	_, ok := c.Get("prefix:key1")
	assert.False(t, ok)
	_, ok = c.Get("prefix:key2")
	assert.False(t, ok)
}

func TestCache_RemoveExpired(t *testing.T) {
	c := NewCacheWithLRU(10*time.Minute, 10*time.Minute, 100)
	defer c.Close()

	c.Set("expired1", "value1", -time.Second)
	c.Set("expired2", "value2", -time.Second)
	c.Set("valid", "value3", time.Minute)

	time.Sleep(200 * time.Millisecond)

	c.removeExpired()

	_, ok := c.Get("expired1")
	assert.False(t, ok, "expired1 should be removed")
	_, ok = c.Get("expired2")
	assert.False(t, ok, "expired2 should be removed")

	_, ok = c.Get("valid")
	assert.True(t, ok, "valid should still exist")
}

func TestCache_RemoveExpired_Concurrent(t *testing.T) {
	c := NewCacheWithLRU(10*time.Minute, 10*time.Minute, 100)
	defer c.Close()

	for i := 0; i < 50; i++ {
		c.Set("key:"+string(rune(i)), "value", -time.Second)
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			c.removeExpired()
		}()
		go func() {
			defer wg.Done()
			c.Get("key0")
		}()
	}
	wg.Wait()
}
