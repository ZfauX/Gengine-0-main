// internal/pkg/cache/valkey.go
package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

// CacheStore определяет общий интерфейс для кэша.
type CacheStore interface {
	Get(key string) (any, bool)
	Set(key string, value any, ttl time.Duration)
	SetDefault(key string, value any)
	Delete(key string)
	DeleteByPrefix(prefix string)
	Flush()
	GetOrSet(key string, ttl time.Duration, fn func() (any, error)) (any, error)
	GetOrSetString(key string, ttl time.Duration, fn func() (string, error)) (string, error)
	GetOrSetInt(key string, ttl time.Duration, fn func() (int, error)) (int, error)
	GetOrSetFloat64(key string, ttl time.Duration, fn func() (float64, error)) (float64, error)
	GetWithCtx(ctx context.Context, key string) (any, bool)
	SetWithCtx(ctx context.Context, key string, value any, ttl time.Duration)
	DeleteWithCtx(ctx context.Context, key string)
	DeleteByPrefixWithCtx(ctx context.Context, prefix string)
}

type ValkeyCache struct {
	client *redis.Client
}

var _ CacheStore = (*ValkeyCache)(nil)

func NewValkeyCache(host, port, password string) CacheStore {
	addr := fmt.Sprintf("%s:%s", host, port)

	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       0,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		log.Warn().Err(err).Str("addr", addr).Msg("Valkey: failed to connect, cache disabled")
		return nil
	}

	log.Info().Str("addr", addr).Msg("Valkey: connected successfully")

	return &ValkeyCache{
		client: client,
	}
}

func (c *ValkeyCache) Get(key string) (any, bool) {
	if c == nil || c.client == nil {
		return nil, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	valBytes, err := c.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, false
	}
	if err != nil {
		log.Warn().Err(err).Str("key", key).Msg("Valkey: Get error")
		return nil, false
	}

	var result any
	if err := json.Unmarshal(valBytes, &result); err == nil {
		return result, true
	}
	return string(valBytes), true
}

func (c *ValkeyCache) Set(key string, value any, ttl time.Duration) {
	if c == nil || c.client == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	data, err := json.Marshal(value)
	if err != nil {
		log.Warn().Err(err).Str("key", key).Msg("Valkey: JSON marshal error, skipping set")
		return
	}

	if err := c.client.Set(ctx, key, data, ttl).Err(); err != nil {
		log.Warn().Err(err).Str("key", key).Msg("Valkey: Set error")
	}
}

func (c *ValkeyCache) SetDefault(key string, value any) {
	c.Set(key, value, 5*time.Minute)
}

func (c *ValkeyCache) Delete(key string) {
	if c == nil || c.client == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.client.Del(ctx, key).Err(); err != nil {
		log.Warn().Err(err).Str("key", key).Msg("Valkey: Delete error")
	}
}

func (c *ValkeyCache) GetWithCtx(ctx context.Context, key string) (any, bool) {
	if c == nil || c.client == nil {
		return nil, false
	}

	valBytes, err := c.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, false
	}
	if err != nil {
		log.Warn().Err(err).Str("key", key).Msg("Valkey: GetWithCtx error")
		return nil, false
	}

	var result any
	if err := json.Unmarshal(valBytes, &result); err == nil {
		return result, true
	}
	return string(valBytes), true
}

func (c *ValkeyCache) SetWithCtx(ctx context.Context, key string, value any, ttl time.Duration) {
	if c == nil || c.client == nil {
		return
	}

	data, err := json.Marshal(value)
	if err != nil {
		log.Warn().Err(err).Str("key", key).Msg("Valkey: JSON marshal error, skipping set")
		return
	}

	if err := c.client.Set(ctx, key, data, ttl).Err(); err != nil {
		log.Warn().Err(err).Str("key", key).Msg("Valkey: SetWithCtx error")
	}
}

func (c *ValkeyCache) DeleteWithCtx(ctx context.Context, key string) {
	if c == nil || c.client == nil {
		return
	}

	if err := c.client.Del(ctx, key).Err(); err != nil {
		log.Warn().Err(err).Str("key", key).Msg("Valkey: DeleteWithCtx error")
	}
}

func (c *ValkeyCache) DeleteByPrefixWithCtx(ctx context.Context, prefix string) {
	if c == nil || c.client == nil {
		return
	}

	var keys []string
	const maxKeys = 10000
	iter := c.client.Scan(ctx, 0, prefix+"*", int64(maxKeys)).Iterator()
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
		if len(keys) >= 100 {
			if err := c.client.Del(ctx, keys...).Err(); err != nil {
				log.Warn().Err(err).Str("prefix", prefix).Msg("Valkey: DeleteByPrefixWithCtx batch error")
			}
			keys = keys[:0]
		}
	}
	if err := iter.Err(); err != nil {
		log.Warn().Err(err).Str("prefix", prefix).Msg("Valkey: DeleteByPrefixWithCtx scan error")
	}

	if len(keys) > 0 {
		if err := c.client.Del(ctx, keys...).Err(); err != nil {
			log.Warn().Err(err).Str("prefix", prefix).Msg("Valkey: DeleteByPrefixWithCtx error")
		}
	}
}

func (c *ValkeyCache) DeleteByPrefix(prefix string) {
	if c == nil || c.client == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var keys []string
	iter := c.client.Scan(ctx, 0, prefix+"*", 0).Iterator()
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
		if len(keys) >= 100 {
			if err := c.client.Del(ctx, keys...).Err(); err != nil {
				log.Warn().Err(err).Str("prefix", prefix).Msg("Valkey: DeleteByPrefix batch error")
			}
			keys = keys[:0]
		}
	}
	if err := iter.Err(); err != nil {
		log.Warn().Err(err).Str("prefix", prefix).Msg("Valkey: DeleteByPrefix scan error")
	}

	if len(keys) > 0 {
		if err := c.client.Del(ctx, keys...).Err(); err != nil {
			log.Warn().Err(err).Str("prefix", prefix).Msg("Valkey: DeleteByPrefix final batch error")
		}
	}
}

func (c *ValkeyCache) Flush() {
	if c == nil || c.client == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := c.client.FlushDB(ctx).Err(); err != nil {
		log.Error().Err(err).Msg("Valkey: Flush error")
	}
}

func (c *ValkeyCache) GetOrSet(key string, ttl time.Duration, fn func() (any, error)) (any, error) {
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

func (c *ValkeyCache) GetOrSetString(key string, ttl time.Duration, fn func() (string, error)) (string, error) {
	val, err := c.GetOrSet(key, ttl, func() (any, error) {
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

func (c *ValkeyCache) GetOrSetInt(key string, ttl time.Duration, fn func() (int, error)) (int, error) {
	val, err := c.GetOrSet(key, ttl, func() (any, error) {
		return fn()
	})
	if err != nil {
		return 0, err
	}
	if i, ok := val.(float64); ok {
		return int(i), nil
	}
	return 0, fmt.Errorf("unexpected type for key %s", key)
}

func (c *ValkeyCache) GetOrSetFloat64(key string, ttl time.Duration, fn func() (float64, error)) (float64, error) {
	val, err := c.GetOrSet(key, ttl, func() (any, error) {
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

func (c *ValkeyCache) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.Close()
}

func (c *ValkeyCache) IsAvailable() bool {
	if c == nil || c.client == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return c.client.Ping(ctx).Err() == nil
}
