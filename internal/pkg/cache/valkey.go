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
	GetOrSetStringWithTTLWithCtx(ctx context.Context, key string, ttl time.Duration, fn func() (string, error)) (string, error)
	ExtendTTL(key string, ttl time.Duration) bool
	GetOrSetInt(key string, ttl time.Duration, fn func() (int, error)) (int, error)
	GetOrSetFloat64(key string, ttl time.Duration, fn func() (float64, error)) (float64, error)
	GetWithCtx(ctx context.Context, key string) (any, bool)
	SetWithCtx(ctx context.Context, key string, value any, ttl time.Duration)
	DeleteWithCtx(ctx context.Context, key string)
	DeleteByPrefixWithCtx(ctx context.Context, prefix string)
	GetOrSetWithCtx(ctx context.Context, key string, ttl time.Duration, fn func() (any, error)) (any, error)
	GetOrSetIntWithCtx(ctx context.Context, key string, ttl time.Duration, fn func() (int, error)) (int, error)
	GetOrSetFloat64WithCtx(ctx context.Context, key string, ttl time.Duration, fn func() (float64, error)) (float64, error)
	ExtendTTLWithCtx(ctx context.Context, key string, ttl time.Duration) bool
	FlushWithCtx(ctx context.Context)
}

type ValkeyCache struct {
	client *redis.Client
}

var _ CacheStore = (*ValkeyCache)(nil)

func NewValkeyClient(host, port, password string, poolSize, minIdleConns, maxRetries int) *redis.Client {
	addr := fmt.Sprintf("%s:%s", host, port)
	client := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           0,
		PoolSize:     poolSize,
		MinIdleConns: minIdleConns,
		MaxRetries:   maxRetries,
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

func NewValkeyCache(host, port, password string, poolSize, minIdleConns, maxRetries int) CacheStore {
	client := NewValkeyClient(host, port, password, poolSize, minIdleConns, maxRetries)
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

// GetOrSetStringWithTTL — атомарная операция get-or-set с Lua скриптом.
// При cache hit НЕ перезаписывает TTL ключа, предотвращая race condition.
const getOrSetLuaScript = `
local key = KEYS[1]
local ttl = tonumber(ARGV[1])
local value = ARGV[2]

local existing = redis.call('GET', key)
if existing then
    return {1, existing}
else
    redis.call('SET', key, value, 'EX', ttl)
    return {0, value}
end
`

func (c *ValkeyCache) GetOrSetStringWithTTL(key string, ttl time.Duration, fn func() (string, error)) (string, error) {
	return c.GetOrSetStringWithTTLWithCtx(context.Background(), key, ttl, fn)
}

// GetOrSetStringWithTTLWithCtx — атомарная операция get-or-set с Lua скриптом и контекстом.
// При cache hit НЕ перезаписывает TTL ключа, предотвращая race condition.
func (c *ValkeyCache) GetOrSetStringWithTTLWithCtx(ctx context.Context, key string, ttl time.Duration, fn func() (string, error)) (string, error) {
	if c == nil || c.client == nil {
		return fn()
	}

	script := c.client.Eval(ctx, getOrSetLuaScript, []string{key}, int(ttl.Seconds()), "")

	result, err := script.Result()
	if err != nil {
		if err == redis.Nil {
			str, fnErr := fn()
			if fnErr != nil {
				return "", fnErr
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
		log.Warn().Err(err).Str("key", key).Msg("Valkey: GetOrSetStringWithTTL script error")
		str, fnErr := fn()
		if fnErr != nil {
			return "", fnErr
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

	if arr, ok := result.([]interface{}); ok && len(arr) >= 2 {
		if s, ok := arr[1].(string); ok {
			return s, nil
		}
	}

	str, err := fn()
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(str)
	if err != nil {
		return "", err
	}
	if setErr := c.client.Set(ctx, key, data, ttl).Err(); setErr != nil {
		log.Warn().Err(setErr).Str("key", key).Msg("Valkey: SetWithTTL error")
	}
	return str, nil
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
	switch v := val.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	case json.Number:
		i, parseErr := v.Int64()
		if parseErr != nil {
			return 0, fmt.Errorf("unexpected type for key %s: cannot parse json.Number", key)
		}
		return int(i), nil
	default:
		return 0, fmt.Errorf("unexpected type for key %s, got %T", key, val)
	}
}

func (c *ValkeyCache) GetOrSetFloat64(key string, ttl time.Duration, fn func() (float64, error)) (float64, error) {
	val, err := c.GetOrSet(key, ttl, func() (any, error) {
		return fn()
	})
	if err != nil {
		return 0, err
	}
	switch v := val.(type) {
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case json.Number:
		f, parseErr := v.Float64()
		if parseErr != nil {
			return 0, fmt.Errorf("unexpected type for key %s: cannot parse json.Number", key)
		}
		return f, nil
	default:
		return 0, fmt.Errorf("unexpected type for key %s, got %T", key, val)
	}
}

// GetOrSetWithCtx — контекстно-ориентированная версия GetOrSet с таймаутом.
func (c *ValkeyCache) GetOrSetWithCtx(ctx context.Context, key string, ttl time.Duration, fn func() (any, error)) (any, error) {
	valBytes, err := c.client.Get(ctx, key).Bytes()
	if err == nil {
		var result any
		if jsonErr := json.Unmarshal(valBytes, &result); jsonErr == nil {
			return result, nil
		}
		return string(valBytes), nil
	}
	if err != redis.Nil {
		log.Warn().Err(err).Str("key", key).Msg("Valkey: GetOrSetWithCtx Get error")
		return fn()
	}

	val, err := fn()
	if err != nil {
		return nil, err
	}
	data, marshalErr := json.Marshal(val)
	if marshalErr != nil {
		return nil, marshalErr
	}
	if setErr := c.client.Set(ctx, key, data, ttl).Err(); setErr != nil {
		log.Warn().Err(setErr).Str("key", key).Msg("Valkey: GetOrSetWithCtx Set error")
	}
	return val, nil
}

// GetOrSetIntWithCtx — контекстно-ориентированная версия GetOrSetInt.
func (c *ValkeyCache) GetOrSetIntWithCtx(ctx context.Context, key string, ttl time.Duration, fn func() (int, error)) (int, error) {
	val, err := c.GetOrSetWithCtx(ctx, key, ttl, func() (any, error) {
		return fn()
	})
	if err != nil {
		return 0, err
	}
	switch v := val.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	case json.Number:
		i, parseErr := v.Int64()
		if parseErr != nil {
			return 0, fmt.Errorf("unexpected type for key %s: cannot parse json.Number", key)
		}
		return int(i), nil
	default:
		return 0, fmt.Errorf("unexpected type for key %s, got %T", key, val)
	}
}

// GetOrSetFloat64WithCtx — контекстно-ориентированная версия GetOrSetFloat64.
func (c *ValkeyCache) GetOrSetFloat64WithCtx(ctx context.Context, key string, ttl time.Duration, fn func() (float64, error)) (float64, error) {
	val, err := c.GetOrSetWithCtx(ctx, key, ttl, func() (any, error) {
		return fn()
	})
	if err != nil {
		return 0, err
	}
	switch v := val.(type) {
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case json.Number:
		f, parseErr := v.Float64()
		if parseErr != nil {
			return 0, fmt.Errorf("unexpected type for key %s: cannot parse json.Number", key)
		}
		return f, nil
	default:
		return 0, fmt.Errorf("unexpected type for key %s, got %T", key, val)
	}
}

// ExtendTTLWithCtx — продлевает TTL с контекстом.
func (c *ValkeyCache) ExtendTTLWithCtx(ctx context.Context, key string, ttl time.Duration) bool {
	if c == nil || c.client == nil {
		return false
	}
	if err := c.client.Expire(ctx, key, ttl).Err(); err != nil {
		if err == redis.Nil {
			return false
		}
		log.Warn().Err(err).Str("key", key).Msg("Valkey: ExtendTTLWithCtx error")
		return false
	}
	return true
}

// FlushWithCtx — очистка кеша с контекстом.
func (c *ValkeyCache) FlushWithCtx(ctx context.Context) {
	if c == nil || c.client == nil {
		return
	}
	if err := c.client.FlushDB(ctx).Err(); err != nil {
		log.Error().Err(err).Msg("Valkey: FlushWithCtx error")
	}
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
