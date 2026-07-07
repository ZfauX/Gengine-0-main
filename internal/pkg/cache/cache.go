// internal/pkg/cache/cache.go
package cache

import (
	"strings"
	"time"

	gocache "github.com/patrickmn/go-cache"
)

// Cache предоставляет общий интерфейс для кэширования с TTL.
type Cache struct {
	store *gocache.Cache
}

// NewCache создаёт новый экземпляр кэша с указанным TTL по умолчанию и интервалом очистки.
func NewCache(defaultTTL, cleanupInterval time.Duration) *Cache {
	return &Cache{
		store: gocache.New(defaultTTL, cleanupInterval),
	}
}

// Get возвращает значение из кэша по ключу.
// Если ключ не найден, возвращает nil и false.
func (c *Cache) Get(key string) (interface{}, bool) {
	return c.store.Get(key)
}

// Set сохраняет значение в кэше с указанным TTL.
// Если ttl == 0, используется TTL по умолчанию.
func (c *Cache) Set(key string, value interface{}, ttl time.Duration) {
	c.store.Set(key, value, ttl)
}

// SetDefault сохраняет значение в кэше с TTL по умолчанию.
func (c *Cache) SetDefault(key string, value interface{}) {
	c.store.SetDefault(key, value)
}

// Delete удаляет значение из кэша по ключу.
func (c *Cache) Delete(key string) {
	c.store.Delete(key)
}

// DeleteByPrefix удаляет все значения из кэша, ключи которых начинаются с prefix.
// go-cache не поддерживает удаление по префиксу нативно, поэтому используем Items() для обхода.
// Важно: Items() возвращает копию map, поэтому итерация безопасна.
func (c *Cache) DeleteByPrefix(prefix string) {
	items := c.store.Items()
	for key := range items {
		if strings.HasPrefix(key, prefix) {
			c.store.Delete(key)
		}
	}
}

// Flush очищает весь кэш.
func (c *Cache) Flush() {
	c.store.Flush()
}

// GetOrSet возвращает значение из кэша, если оно существует,
// иначе вызывает функцию fn, сохраняет результат в кэш и возвращает его.
// fn должна возвращать значение и ошибку.
func (c *Cache) GetOrSet(key string, ttl time.Duration, fn func() (interface{}, error)) (interface{}, error) {
	if val, ok := c.Get(key); ok {
		return val, nil
	}
	val, err := fn()
	if err != nil {
		return nil, err
	}
	if ttl == 0 {
		c.SetDefault(key, val)
	} else {
		c.Set(key, val, ttl)
	}
	return val, nil
}

// GetOrSetString — удобная обёртка для GetOrSet со строковым значением.
func (c *Cache) GetOrSetString(key string, ttl time.Duration, fn func() (string, error)) (string, error) {
	val, err := c.GetOrSet(key, ttl, func() (interface{}, error) {
		return fn()
	})
	if err != nil {
		return "", err
	}
	return val.(string), nil
}

// GetOrSetInt — удобная обёртка для GetOrSet с int.
func (c *Cache) GetOrSetInt(key string, ttl time.Duration, fn func() (int, error)) (int, error) {
	val, err := c.GetOrSet(key, ttl, func() (interface{}, error) {
		return fn()
	})
	if err != nil {
		return 0, err
	}
	return val.(int), nil
}

// GetOrSetFloat64 — удобная обёртка для GetOrSet с float64.
func (c *Cache) GetOrSetFloat64(key string, ttl time.Duration, fn func() (float64, error)) (float64, error) {
	val, err := c.GetOrSet(key, ttl, func() (interface{}, error) {
		return fn()
	})
	if err != nil {
		return 0, err
	}
	return val.(float64), nil
}
