// internal/pkg/cache/cache.go
package cache

import (
	"context"
	"strings"
	"sync"
	"time"

	gocache "github.com/patrickmn/go-cache"
)

// Cache предоставляет общий интерфейс для кэширования с TTL.
// Реализует интерфейс CacheStore.
type Cache struct {
	store      *gocache.Cache
	prefixLock sync.RWMutex
	prefixKeys map[string]map[string]bool // prefix -> set of keys
}

var _ CacheStore = (*Cache)(nil)

// NewCache создаёт новый экземпляр кэша с указанным TTL по умолчанию и интервалом очистки.
func NewCache(defaultTTL, cleanupInterval time.Duration) *Cache {
	return &Cache{
		store:      gocache.New(defaultTTL, cleanupInterval),
		prefixKeys: make(map[string]map[string]bool),
	}
}

// Get возвращает значение из кэша по ключу.
// Если ключ не найден, возвращает nil и false.
func (c *Cache) Get(key string) (interface{}, bool) {
	return c.store.Get(key)
}

// Set сохраняет значение в кэше с указанным TTL.
// Если ttl == 0, используется TTL по умолчанию.
// Отслеживает префиксы для быстрого DeleteByPrefix.
func (c *Cache) Set(key string, value interface{}, ttl time.Duration) {
	c.store.Set(key, value, ttl)
	c.trackPrefix(key)
}

// SetDefault сохраняет значение в кэше с TTL по умолчанию.
// Отслеживает префиксы для быстрого DeleteByPrefix.
func (c *Cache) SetDefault(key string, value interface{}) {
	c.store.SetDefault(key, value)
	c.trackPrefix(key)
}

// Delete удаляет значение из кэша по ключу.
// Удаляет ключ из индекса префиксов.
func (c *Cache) Delete(key string) {
	c.store.Delete(key)
	c.prefixLock.Lock()
	for _, keys := range c.prefixKeys {
		delete(keys, key)
	}
	c.prefixLock.Unlock()
}

// trackPrefix добавляет ключ в индекс префиксов.
func (c *Cache) trackPrefix(key string) {
	c.prefixLock.Lock()
	defer c.prefixLock.Unlock()

	// Извлекаем все возможные префиксы (до 3 уровней)
	parts := strings.Split(key, ":")
	for i := range parts {
		prefix := strings.Join(parts[:i+1], ":")
		if c.prefixKeys[prefix] == nil {
			c.prefixKeys[prefix] = make(map[string]bool)
		}
		c.prefixKeys[prefix][key] = true
	}
}

// DeleteByPrefix удаляет все значения из кэша, ключи которых начинаются с prefix.
// Оптимизация: использует индекс префиксов вместо полного обхода map.
func (c *Cache) DeleteByPrefix(prefix string) {
	c.prefixLock.RLock()
	keys, exists := c.prefixKeys[prefix]
	c.prefixLock.RUnlock()

	if !exists || len(keys) == 0 {
		return
	}

	// Удаляем ключи из основного кэша
	for key := range keys {
		c.store.Delete(key)
	}

	// Удаляем индекс префикса и все вложенные префиксы
	c.prefixLock.Lock()
	delete(c.prefixKeys, prefix)
	for key := range keys {
		parts := strings.Split(key, ":")
		for i := range parts {
			p := strings.Join(parts[:i+1], ":")
			if p != prefix && c.prefixKeys[p] != nil {
				delete(c.prefixKeys[p], key)
			}
		}
	}
	c.prefixLock.Unlock()
}

// Flush очищает весь кэш.
func (c *Cache) Flush() {
	c.store.Flush()
}

// GetWithCtx возвращает значение из кэша по ключу с контекстом.
// Контекст игнорируется для in-memory кэша.
func (c *Cache) GetWithCtx(ctx context.Context, key string) (interface{}, bool) {
	return c.store.Get(key)
}

// SetWithCtx сохраняет значение в кэше с контекстом.
// Контекст игнорируется для in-memory кэша.
func (c *Cache) SetWithCtx(ctx context.Context, key string, value interface{}, ttl time.Duration) {
	c.store.Set(key, value, ttl)
}

// DeleteWithCtx удаляет значение из кэша с контекстом.
// Контекст игнорируется для in-memory кэша.
func (c *Cache) DeleteWithCtx(ctx context.Context, key string) {
	c.store.Delete(key)
}

// DeleteByPrefixWithCtx удаляет все ключи с префиксом с контекстом.
// Контекст игнорируется для in-memory кэша.
func (c *Cache) DeleteByPrefixWithCtx(ctx context.Context, prefix string) {
	c.DeleteByPrefix(prefix)
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
