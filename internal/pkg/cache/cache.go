// internal/pkg/cache/cache.go
package cache

import (
	"context"
	"strings"
	"sync"
	"time"

	gocache "github.com/patrickmn/go-cache"
)

type Cache struct {
	store      *gocache.Cache
	prefixLock sync.RWMutex
	prefixKeys map[string]map[string]bool
}

func NewCache(defaultTTL, cleanupInterval time.Duration) *Cache {
	return &Cache{
		store:      gocache.New(defaultTTL, cleanupInterval),
		prefixKeys: make(map[string]map[string]bool),
	}
}

func (c *Cache) Get(key string) (any, bool) {
	return c.store.Get(key)
}

func (c *Cache) Set(key string, value any, ttl time.Duration) {
	c.store.Set(key, value, ttl)
	c.trackPrefix(key)
}

func (c *Cache) SetDefault(key string, value any) {
	c.store.SetDefault(key, value)
	c.trackPrefix(key)
}

func (c *Cache) Delete(key string) {
	c.store.Delete(key)
	c.prefixLock.Lock()
	for _, keys := range c.prefixKeys {
		delete(keys, key)
	}
	c.prefixLock.Unlock()
}

func (c *Cache) trackPrefix(key string) {
	c.prefixLock.Lock()
	defer c.prefixLock.Unlock()

	parts := strings.Split(key, ":")
	for i := range parts {
		prefix := strings.Join(parts[:i+1], ":")
		if c.prefixKeys[prefix] == nil {
			c.prefixKeys[prefix] = make(map[string]bool)
		}
		c.prefixKeys[prefix][key] = true
	}
}

func (c *Cache) DeleteByPrefix(prefix string) {
	c.prefixLock.RLock()
	keys, exists := c.prefixKeys[prefix]
	c.prefixLock.RUnlock()

	if !exists || len(keys) == 0 {
		return
	}

	for key := range keys {
		c.store.Delete(key)
	}

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

func (c *Cache) Flush() {
	c.store.Flush()
}

func (c *Cache) GetWithCtx(ctx context.Context, key string) (any, bool) {
	return c.store.Get(key)
}

func (c *Cache) SetWithCtx(ctx context.Context, key string, value any, ttl time.Duration) {
	c.store.Set(key, value, ttl)
}

func (c *Cache) DeleteWithCtx(ctx context.Context, key string) {
	c.store.Delete(key)
}

func (c *Cache) DeleteByPrefixWithCtx(ctx context.Context, prefix string) {
	c.DeleteByPrefix(prefix)
}

func (c *Cache) GetOrSet(key string, ttl time.Duration, fn func() (any, error)) (any, error) {
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

func (c *Cache) GetOrSetString(key string, ttl time.Duration, fn func() (string, error)) (string, error) {
	val, err := c.GetOrSet(key, ttl, func() (any, error) {
		return fn()
	})
	if err != nil {
		return "", err
	}
	return val.(string), nil
}

func (c *Cache) GetOrSetInt(key string, ttl time.Duration, fn func() (int, error)) (int, error) {
	val, err := c.GetOrSet(key, ttl, func() (any, error) {
		return fn()
	})
	if err != nil {
		return 0, err
	}
	return val.(int), nil
}

func (c *Cache) GetOrSetFloat64(key string, ttl time.Duration, fn func() (float64, error)) (float64, error) {
	val, err := c.GetOrSet(key, ttl, func() (any, error) {
		return fn()
	})
	if err != nil {
		return 0, err
	}
	return val.(float64), nil
}

func (c *Cache) Close() error {
	c.Flush()
	return nil
}
