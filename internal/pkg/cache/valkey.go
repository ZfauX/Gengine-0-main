// internal/pkg/cache/valkey.go
package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

// CacheStore определяет общий интерфейс для кэша.
type CacheStore interface {
	Get(key string) (interface{}, bool)
	Set(key string, value interface{}, ttl time.Duration)
	SetDefault(key string, value interface{})
	Delete(key string)
	DeleteByPrefix(prefix string)
	Flush()
	GetOrSet(key string, ttl time.Duration, fn func() (interface{}, error)) (interface{}, error)
	GetOrSetString(key string, ttl time.Duration, fn func() (string, error)) (string, error)
	GetOrSetInt(key string, ttl time.Duration, fn func() (int, error)) (int, error)
	GetOrSetFloat64(key string, ttl time.Duration, fn func() (float64, error)) (float64, error)
}

// ValkeyCache предоставляет интерфейс кэширования через Valkey (Redis-compatible).
// Реализует интерфейс CacheStore.
type ValkeyCache struct {
	client *redis.Client
	ctx    context.Context
}

var _ CacheStore = (*ValkeyCache)(nil)

// NewValkeyCache создаёт новый клиент Valkey.
// Возвращает nil, если подключение невозможно.
func NewValkeyCache(host, port, password string) CacheStore {
	addr := fmt.Sprintf("%s:%s", host, port)

	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       0, // используем базу 0 по умолчанию
	})

	ctx := context.Background()

	// Проверяем подключение
	if err := client.Ping(ctx).Err(); err != nil {
		log.Warn().Err(err).Str("addr", addr).Msg("Valkey: failed to connect, cache disabled")
		return nil
	}

	log.Info().Str("addr", addr).Msg("Valkey: connected successfully")

	return &ValkeyCache{
		client: client,
		ctx:    ctx,
	}
}

// Get возвращает значение из кэша по ключу.
func (c *ValkeyCache) Get(key string) (interface{}, bool) {
	if c == nil || c.client == nil {
		return nil, false
	}

	val, err := c.client.Get(c.ctx, key).Bytes()
	if err == redis.Nil {
		return nil, false
	}
	if err != nil {
		log.Warn().Err(err).Str("key", key).Msg("Valkey: Get error")
		return nil, false
	}

	return val, true
}

// Set сохраняет значение в кэше с указанным TTL.
func (c *ValkeyCache) Set(key string, value interface{}, ttl time.Duration) {
	if c == nil || c.client == nil {
		return
	}

	data, ok := value.([]byte)
	if !ok {
		log.Warn().Str("key", key).Msg("Valkey: value is not []byte")
		return
	}

	if err := c.client.Set(c.ctx, key, data, ttl).Err(); err != nil {
		log.Warn().Err(err).Str("key", key).Msg("Valkey: Set error")
	}
}

// SetDefault сохраняет значение в кэше с TTL по умолчанию (5 минут).
func (c *ValkeyCache) SetDefault(key string, value interface{}) {
	c.Set(key, value, 5*time.Minute)
}

// Delete удаляет значение из кэша по ключу.
func (c *ValkeyCache) Delete(key string) {
	if c == nil || c.client == nil {
		return
	}

	if err := c.client.Del(c.ctx, key).Err(); err != nil {
		log.Warn().Err(err).Str("key", key).Msg("Valkey: Delete error")
	}
}

// DeleteByPrefix удаляет все значения из кэша, ключи которых начинаются с prefix.
// Использует SCAN для безопасного перебора ключей.
func (c *ValkeyCache) DeleteByPrefix(prefix string) {
	if c == nil || c.client == nil {
		return
	}

	iter := c.client.Scan(c.ctx, 0, prefix+"*", 0).Iterator()
	for iter.Next(c.ctx) {
		if err := c.client.Del(c.ctx, iter.Val()).Err(); err != nil {
			log.Warn().Err(err).Str("key", iter.Val()).Msg("Valkey: DeleteByPrefix error")
		}
	}
	if err := iter.Err(); err != nil {
		log.Warn().Err(err).Str("prefix", prefix).Msg("Valkey: DeleteByPrefix scan error")
	}
}

// Flush очищает весь кэш (используйте с осторожностью!).
func (c *ValkeyCache) Flush() {
	if c == nil || c.client == nil {
		return
	}

	if err := c.client.FlushDB(c.ctx).Err(); err != nil {
		log.Error().Err(err).Msg("Valkey: Flush error")
	}
}

// GetOrSet возвращает значение из кэша, если оно существует,
// иначе вызывает функцию fn, сохраняет результат в кэш и возвращает его.
func (c *ValkeyCache) GetOrSet(key string, ttl time.Duration, fn func() (interface{}, error)) (interface{}, error) {
	if val, ok := c.Get(key); ok {
		return val, nil
	}
	val, err := fn()
	if err != nil {
		return nil, err
	}
	c.Set(key, val, ttl)
	return val, nil
}

// GetOrSetString — удобная обёртка для GetOrSet со строковым значением.
func (c *ValkeyCache) GetOrSetString(key string, ttl time.Duration, fn func() (string, error)) (string, error) {
	val, err := c.GetOrSet(key, ttl, func() (interface{}, error) {
		return fn()
	})
	if err != nil {
		return "", err
	}
	if str, ok := val.(string); ok {
		return str, nil
	}
	return "", fmt.Errorf("unexpected type for key %s", key)
}

// GetOrSetInt — удобная обёртка для GetOrSet с int.
func (c *ValkeyCache) GetOrSetInt(key string, ttl time.Duration, fn func() (int, error)) (int, error) {
	val, err := c.GetOrSet(key, ttl, func() (interface{}, error) {
		return fn()
	})
	if err != nil {
		return 0, err
	}
	if i, ok := val.(int); ok {
		return i, nil
	}
	return 0, fmt.Errorf("unexpected type for key %s", key)
}

// GetOrSetFloat64 — удобная обёртка для GetOrSet с float64.
func (c *ValkeyCache) GetOrSetFloat64(key string, ttl time.Duration, fn func() (float64, error)) (float64, error) {
	val, err := c.GetOrSet(key, ttl, func() (interface{}, error) {
		return fn()
	})
	if err != nil {
		return 0, err
	}
	if f, ok := val.(float64); ok {
		return f, nil
	}
	return 0, fmt.Errorf("unexpected type for key %s", key)
}

// Close закрывает соединение с Valkey.
func (c *ValkeyCache) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.Close()
}

// IsAvailable проверяет доступность кэша.
func (c *ValkeyCache) IsAvailable() bool {
	if c == nil || c.client == nil {
		return false
	}
	return c.client.Ping(c.ctx).Err() == nil
}
