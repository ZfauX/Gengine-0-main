package cache

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"

	"github.com/rs/zerolog/log"
)

type cacheItem struct {
	value   any
	expires time.Time
}

func (i cacheItem) expired() bool {
	return !i.expires.IsZero() && time.Now().After(i.expires)
}

type Cache struct {
	mu          sync.RWMutex
	lru         *lru.Cache[string, cacheItem]
	prefixLock  sync.RWMutex
	prefixKeys  map[string]map[string]bool
	keyPrefixes map[string]map[string]bool
	maxSize     int
	stop        chan struct{}
}

func NewCache(defaultTTL, cleanupInterval time.Duration) (*Cache, error) {
	c := NewCacheWithLRU(defaultTTL, cleanupInterval, 0)
	if c == nil {
		return nil, fmt.Errorf("cache: LRU creation failed")
	}
	c.startCleanup(cleanupInterval)
	return c, nil
}

func NewCacheWithLRU(defaultTTL, cleanupInterval time.Duration, maxSize int) *Cache {
	size := maxSize
	if size <= 0 {
		size = math.MaxInt
	}
	c := &Cache{
		prefixKeys:  make(map[string]map[string]bool),
		keyPrefixes: make(map[string]map[string]bool),
		maxSize:     maxSize,
		stop:        make(chan struct{}),
	}
	evictCallback := func(key string, _ cacheItem) {
		c.prefixLock.Lock()
		if prefixes, ok := c.keyPrefixes[key]; ok {
			for p := range prefixes {
				delete(c.prefixKeys[p], key)
			}
			delete(c.keyPrefixes, key)
		}
		c.prefixLock.Unlock()
	}

	lruCache, err := lru.NewWithEvict[string, cacheItem](size, evictCallback)
	if err != nil {
		log.Error().Err(err).Int("requested_size", size).Msg("cache: LRU creation failed, using unlimited size")
		// Fallback to max size (never fails)
		lruCache, err = lru.NewWithEvict[string, cacheItem](math.MaxInt, evictCallback)
		if err != nil {
			log.Error().Err(err).Msg("cache: LRU creation failed even with unlimited size")
			return nil
		}
	}
	c.lru = lruCache
	return c
}

func (c *Cache) startCleanup(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-c.stop:
				return
			case <-ticker.C:
				c.removeExpired()
			}
		}
	}()
}

func (c *Cache) removeExpired() {
	now := time.Now()
	var toRemove []string

	c.mu.RLock()
	for _, key := range c.lru.Keys() {
		item, ok := c.lru.Get(key)
		if ok && !item.expires.IsZero() && now.After(item.expires) {
			toRemove = append(toRemove, key)
		}
	}
	c.mu.RUnlock()

	if len(toRemove) == 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for _, key := range toRemove {
		if item, ok := c.lru.Get(key); ok && !item.expires.IsZero() && now.After(item.expires) {
			c.lru.Remove(key)
		}
	}
}

func (c *Cache) Get(key string) (any, bool) {
	c.mu.RLock()
	item, ok := c.lru.Get(key)
	if !ok {
		c.mu.RUnlock()
		return nil, false
	}
	if item.expired() {
		c.mu.RUnlock()
		c.mu.Lock()
		item, ok = c.lru.Get(key)
		if ok {
			if item.expired() {
				c.lru.Remove(key)
				c.mu.Unlock()
				return nil, false
			}
			c.mu.Unlock()
			return item.value, true
		}
		c.mu.Unlock()
		return nil, false
	}
	c.mu.RUnlock()
	return item.value, true
}

func (c *Cache) Set(key string, value any, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lru.Add(key, cacheItem{value: value, expires: time.Now().Add(ttl)})
	c.trackPrefix(key)
}

const defaultCacheTTL = 5 * time.Minute

func (c *Cache) SetDefault(key string, value any) {
	c.Set(key, value, defaultCacheTTL)
}

func (c *Cache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lru.Remove(key)
	c.prefixLock.Lock()
	if prefixes, ok := c.keyPrefixes[key]; ok {
		for p := range prefixes {
			delete(c.prefixKeys[p], key)
		}
		delete(c.keyPrefixes, key)
	}
	c.prefixLock.Unlock()
}

func (c *Cache) trackPrefix(key string) {
	c.prefixLock.Lock()
	defer c.prefixLock.Unlock()

	parts := strings.Split(key, ":")
	prefixes := make(map[string]bool)
	for i := range parts {
		prefix := strings.Join(parts[:i+1], ":")
		if c.prefixKeys[prefix] == nil {
			c.prefixKeys[prefix] = make(map[string]bool)
		}
		c.prefixKeys[prefix][key] = true
		prefixes[prefix] = true
	}
	if len(prefixes) > 0 {
		c.keyPrefixes[key] = prefixes
	}
}

func (c *Cache) DeleteByPrefix(prefix string) {
	c.prefixLock.RLock()
	keys, exists := c.prefixKeys[prefix]
	if !exists || len(keys) == 0 {
		c.prefixLock.RUnlock()
		return
	}
	// Копируем ключи для удаления
	keysCopy := make([]string, 0, len(keys))
	for key := range keys {
		keysCopy = append(keysCopy, key)
	}
	c.prefixLock.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	for _, key := range keysCopy {
		c.lru.Remove(key)
	}

	delete(c.prefixKeys, prefix)
	for _, key := range keysCopy {
		if keyPrefixes, ok := c.keyPrefixes[key]; ok {
			delete(keyPrefixes, prefix)
			if len(keyPrefixes) == 0 {
				delete(c.keyPrefixes, key)
			}
		}
	}
}

func (c *Cache) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lru.Purge()
	c.prefixLock.Lock()
	c.prefixKeys = make(map[string]map[string]bool)
	c.keyPrefixes = make(map[string]map[string]bool)
	c.prefixLock.Unlock()
}

func (c *Cache) GetWithCtx(_ context.Context, key string) (any, bool) {
	return c.Get(key)
}

func (c *Cache) SetWithCtx(_ context.Context, key string, value any, ttl time.Duration) {
	c.Set(key, value, ttl)
}

func (c *Cache) DeleteWithCtx(_ context.Context, key string) {
	c.Delete(key)
}

func (c *Cache) DeleteByPrefixWithCtx(_ context.Context, prefix string) {
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
	s, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("cached value is not a string for key %s", key)
	}
	return s, nil
}

func (c *Cache) GetOrSetInt(key string, ttl time.Duration, fn func() (int, error)) (int, error) {
	val, err := c.GetOrSet(key, ttl, func() (any, error) {
		return fn()
	})
	if err != nil {
		return 0, err
	}
	i, ok := val.(int)
	if !ok {
		return 0, fmt.Errorf("cached value is not an int for key %s", key)
	}
	return i, nil
}

func (c *Cache) GetOrSetFloat64(key string, ttl time.Duration, fn func() (float64, error)) (float64, error) {
	val, err := c.GetOrSet(key, ttl, func() (any, error) {
		return fn()
	})
	if err != nil {
		return 0, err
	}
	f, ok := val.(float64)
	if !ok {
		return 0, fmt.Errorf("cached value is not a float64 for key %s", key)
	}
	return f, nil
}

func (c *Cache) Close() error {
	close(c.stop)
	c.Flush()
	return nil
}

func (c *Cache) Stats() map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	items := c.lru.Len()
	var utilization float64
	if c.maxSize > 0 {
		utilization = float64(items) / float64(c.maxSize)
	}
	return map[string]any{
		"items":       items,
		"max_size":    c.maxSize,
		"utilization": utilization,
	}
}

func (c *Cache) ExtendTTL(key string, ttl time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.lru.Get(key)
	if !ok {
		return false
	}
	item.expires = time.Now().Add(ttl)
	c.lru.Add(key, item)
	return true
}

func (c *Cache) GetOrSetStringWithTTL(key string, ttl time.Duration, fn func() (string, error)) (string, error) {
	return c.GetOrSetString(key, ttl, fn)
}

type NoopCache struct{}

func NewNoopCache() *NoopCache { return &NoopCache{} }

func (NoopCache) Get(_ string) (any, bool)             { return nil, false }
func (NoopCache) Set(_ string, _ any, _ time.Duration) {}
func (NoopCache) SetDefault(_ string, _ any)           {}
func (NoopCache) Delete(_ string)                      {}
func (NoopCache) DeleteByPrefix(_ string)              {}
func (NoopCache) Flush()                               {}
func (NoopCache) GetOrSet(_ string, _ time.Duration, fn func() (any, error)) (any, error) {
	return fn()
}
func (NoopCache) GetOrSetString(_ string, _ time.Duration, fn func() (string, error)) (string, error) {
	return fn()
}
func (NoopCache) GetOrSetStringWithTTL(_ string, _ time.Duration, fn func() (string, error)) (string, error) {
	return fn()
}
func (NoopCache) ExtendTTL(_ string, _ time.Duration) bool { return false }
func (NoopCache) GetOrSetInt(_ string, _ time.Duration, fn func() (int, error)) (int, error) {
	return fn()
}
func (NoopCache) GetOrSetFloat64(_ string, _ time.Duration, fn func() (float64, error)) (float64, error) {
	return fn()
}
func (NoopCache) GetWithCtx(_ context.Context, _ string) (any, bool)             { return nil, false }
func (NoopCache) SetWithCtx(_ context.Context, _ string, _ any, _ time.Duration) {}
func (NoopCache) DeleteWithCtx(_ context.Context, _ string)                      {}
func (NoopCache) DeleteByPrefixWithCtx(_ context.Context, _ string)              {}
func (NoopCache) Close() error                                                   { return nil }
