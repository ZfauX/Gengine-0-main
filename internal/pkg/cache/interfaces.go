package cache

import (
	"context"
	"time"
)

// Getter — операции чтения из кэша.
type Getter interface {
	Get(key string) (any, bool)
	GetWithCtx(ctx context.Context, key string) (any, bool)
}

// Setter — операции записи в кэш.
type Setter interface {
	Set(key string, value any, ttl time.Duration)
	SetDefault(key string, value any)
	SetWithCtx(ctx context.Context, key string, value any, ttl time.Duration)
}

// Deleter — операции удаления из кэша.
type Deleter interface {
	Delete(key string)
	DeleteByPrefix(prefix string)
	Flush()
	DeleteWithCtx(ctx context.Context, key string)
	DeleteByPrefixWithCtx(ctx context.Context, prefix string)
	FlushWithCtx(ctx context.Context)
}

// GetOrSetter — атомарные операции get-or-set.
type GetOrSetter interface {
	GetOrSet(key string, ttl time.Duration, fn func() (any, error)) (any, error)
	GetOrSetString(key string, ttl time.Duration, fn func() (string, error)) (string, error)
	GetOrSetStringWithTTL(key string, ttl time.Duration, fn func() (string, error)) (string, error)
	GetOrSetStringWithTTLWithCtx(ctx context.Context, key string, ttl time.Duration, fn func() (string, error)) (string, error)
	GetOrSetInt(key string, ttl time.Duration, fn func() (int, error)) (int, error)
	GetOrSetFloat64(key string, ttl time.Duration, fn func() (float64, error)) (float64, error)
	GetOrSetWithCtx(ctx context.Context, key string, ttl time.Duration, fn func() (any, error)) (any, error)
	GetOrSetIntWithCtx(ctx context.Context, key string, ttl time.Duration, fn func() (int, error)) (int, error)
	GetOrSetFloat64WithCtx(ctx context.Context, key string, ttl time.Duration, fn func() (float64, error)) (float64, error)
}

// Extender — операции продления TTL ключей.
type Extender interface {
	ExtendTTL(key string, ttl time.Duration) bool
	ExtendTTLWithCtx(ctx context.Context, key string, ttl time.Duration) bool
}
