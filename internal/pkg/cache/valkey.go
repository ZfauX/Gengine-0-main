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

const (
	valkeyOpTimeout       = 5 * time.Second
	valkeyDefaultCacheTTL = 5 * time.Minute
	valkeyBatchOpTimeout  = 10 * time.Second
	valkeyQuickOpTimeout  = 2 * time.Second
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
	GetOrSetStringWithTTL(key string, ttl time.Duration, fn func() (string, error)) (string, error)
	ExtendTTL(key string, ttl time.Duration) bool
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

func NewValkeyClient(host, port, password string) *redis.Client {
	addr := fmt.Sprintf("%s:%s", host, port)
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       0,
	})
	ctx, cancel := context.WithTimeout(context.Background(), valkeyOpTimeout)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		log.Warn().Err(err).Str("addr", addr).Msg("Valkey: failed to connect")
		return nil
	}
	log.Info().Str("addr", addr).Msg("Valkey: connected successfully")
	return client
}

func NewValkeyCache(host, port, password string) CacheStore {
	client := NewValkeyClient(host, port, password)
	if client == nil {
		return nil
	}
	return &ValkeyCache{client: client}
}

func (c *ValkeyCache) Get(key string) (any, bool) {
	if c == nil || c.client == nil {
		return nil, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), valkeyOpTimeout)
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

	ctx, cancel := context.WithTimeout(context.Background(), valkeyOpTimeout)
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
	c.Set(key, value, valkeyDefaultCacheTTL)
}

func (c *ValkeyCache) Delete(key string) {
	if c == nil || c.client == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), valkeyOpTimeout)
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

	var cursor uint64
	const pageSize = 1000
	for {
		keys, nextCursor, err := c.client.Scan(ctx, cursor, prefix+"*", int64(pageSize)).Result()
		if err != nil {
			log.Warn().Err(err).Str("prefix", prefix).Msg("Valkey: DeleteByPrefixWithCtx scan error")
			return
		}
		for _, key := range keys {
			if err := c.client.Del(ctx, key).Err(); err != nil {
				log.Warn().Err(err).Str("prefix", prefix).Msg("Valkey: DeleteByPrefixWithCtx batch error")
			}
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
}

func (c *ValkeyCache) DeleteByPrefix(prefix string) {
	if c == nil || c.client == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), valkeyBatchOpTimeout)
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

	ctx, cancel := context.WithTimeout(context.Background(), valkeyBatchOpTimeout)
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

// GetOrSetStringWithTTL — как GetOrSetString, но при cache hit НЕ перезаписывает
// ключ в Redis. Это устраняет проблему «частой перезаписи» при малом TTL:
// если значение в кэше, оно остаётся на месте и сохраняет свой TTL.
//
// Используется для hot-ключей с TTL < 30s, где каждый set «обнуляет» expiration.
func (c *ValkeyCache) GetOrSetStringWithTTL(key string, ttl time.Duration, fn func() (string, error)) (string, error) {
	if c == nil || c.client == nil {
		return fn()
	}

	ctx, cancel := context.WithTimeout(context.Background(), valkeyOpTimeout)
	defer cancel()

	// Сначала читаем
	valBytes, getErr := c.client.Get(ctx, key).Bytes()
	if getErr == redis.Nil {
		// Cache miss — вычисляем и сохраняем
		str, errFn := fn()
		if errFn != nil {
			return "", errFn
		}
		data, marshalErr := json.Marshal(str)
		if marshalErr != nil {
			return "", marshalErr
		}
		if setErr := c.client.Set(ctx, key, data, ttl).Err(); setErr != nil {
			log.Warn().Err(setErr).Str("key", key).Msg("Valkey: SetWithTTL error")
		}
		return str, nil
	}
	if getErr != nil {
		log.Warn().Err(getErr).Str("key", key).Msg("Valkey: GetWithTTL error")
		return fn() // fallback to compute
	}

	// Cache hit — НЕ перезаписываем, просто возвращаем
	var result any
	if unmarshalErr := json.Unmarshal(valBytes, &result); unmarshalErr == nil {
		if s, ok := result.(string); ok {
			return s, nil
		}
	}
	return string(valBytes), nil
}

// ExtendTTL продлевает TTL существующего ключа. Возвращает true, если ключ найден.
func (c *ValkeyCache) ExtendTTL(key string, ttl time.Duration) bool {
	if c == nil || c.client == nil {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), valkeyOpTimeout)
	defer cancel()

	if err := c.client.Expire(ctx, key, ttl).Err(); err != nil {
		if err == redis.Nil {
			return false
		}
		log.Warn().Err(err).Str("key", key).Msg("Valkey: ExtendTTL error")
		return false
	}
	return true
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
	ctx, cancel := context.WithTimeout(context.Background(), valkeyQuickOpTimeout)
	defer cancel()
	return c.client.Ping(ctx).Err() == nil
}
