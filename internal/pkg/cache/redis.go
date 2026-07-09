// internal/pkg/cache/redis.go
package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisCache — обёртка над Redis для distributed кэширования.
type RedisCache struct {
	client *redis.Client
	prefix string
}

// NewRedisCache создаёт новый Redis cache с префиксом для ключей.
func NewRedisCache(addr, password string, db int, prefix string) *RedisCache {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	return &RedisCache{
		client: client,
		prefix: prefix,
	}
}

// Get возвращает значение из кэша.
func (r *RedisCache) Get(key string) (interface{}, bool) {
	val, err := r.client.Get(context.Background(), r.prefix+key).Result()
	if err == redis.Nil {
		return nil, false
	}
	if err != nil {
		return nil, false
	}
	return val, true
}

// Set сохраняет значение в кэш.
func (r *RedisCache) Set(key string, value interface{}, ttl time.Duration) {
	str, ok := value.(string)
	if !ok {
		return
	}
	r.client.Set(context.Background(), r.prefix+key, str, ttl)
}

// SetDefault сохраняет значение с TTL по умолчанию (1 час).
func (r *RedisCache) SetDefault(key string, value interface{}) {
	r.Set(key, value, 1*time.Hour)
}

// Delete удаляет значение из кэша.
func (r *RedisCache) Delete(key string) {
	r.client.Del(context.Background(), r.prefix+key)
}

// DeleteByPrefix удаляет все ключи с префиксом.
func (r *RedisCache) DeleteByPrefix(prefix string) {
	ctx := context.Background()
	var cursor uint64
	for {
		keys, nextCursor, err := r.client.Scan(ctx, cursor, r.prefix+prefix+"*", 100).Result()
		if err != nil {
			break
		}
		if len(keys) > 0 {
			r.client.Del(ctx, keys...)
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
}

// Flush очищает весь кэш с префиксом.
func (r *RedisCache) Flush() {
	ctx := context.Background()
	var cursor uint64
	for {
		keys, nextCursor, err := r.client.Scan(ctx, cursor, r.prefix+"*", 100).Result()
		if err != nil {
			break
		}
		if len(keys) > 0 {
			r.client.Del(ctx, keys...)
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
}

// GetOrSet возвращает значение из кэша, иначе вызывает fn, сохраняет и возвращает.
func (r *RedisCache) GetOrSet(key string, ttl time.Duration, fn func() (interface{}, error)) (interface{}, error) {
	if val, ok := r.Get(key); ok {
		return val, nil
	}
	val, err := fn()
	if err != nil {
		return nil, err
	}
	r.Set(key, val, ttl)
	return val, nil
}

// GetOrSetString — удобная обёртка для строковых значений.
func (r *RedisCache) GetOrSetString(key string, ttl time.Duration, fn func() (string, error)) (string, error) {
	val, err := r.GetOrSet(key, ttl, func() (interface{}, error) {
		return fn()
	})
	if err != nil {
		return "", err
	}
	return val.(string), nil
}

// GetOrSetInt — удобная обёртка для int.
func (r *RedisCache) GetOrSetInt(key string, ttl time.Duration, fn func() (int, error)) (int, error) {
	val, err := r.GetOrSet(key, ttl, func() (interface{}, error) {
		return fn()
	})
	if err != nil {
		return 0, err
	}
	return val.(int), nil
}

// GetOrSetFloat64 — удобная обёртка для float64.
func (r *RedisCache) GetOrSetFloat64(key string, ttl time.Duration, fn func() (float64, error)) (float64, error) {
	val, err := r.GetOrSet(key, ttl, func() (interface{}, error) {
		return fn()
	})
	if err != nil {
		return 0, err
	}
	return val.(float64), nil
}

// Close закрывает соединение с Redis.
func (r *RedisCache) Close() error {
	return r.client.Close()
}
