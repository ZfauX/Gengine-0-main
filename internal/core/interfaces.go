// internal/core/interfaces.go
package core

import (
	"context"
	"time"
)

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

type Database interface {
	Begin() Database
	Commit() error
	Rollback() error
	Where(query interface{}, args ...interface{}) Database
	First(out interface{}, queries ...interface{}) Database
	Find(out interface{}, queries ...interface{}) Database
	Pluck(column string, dest interface{}) Database
	Count(query *int64) Database
	Updates(values interface{}) Database
	Delete(force ...interface{}) Database
	Creates(datas interface{}) Database
	Raw(sql string, bindings ...interface{}) Database
	Transaction(fc func(tx Database) error) error
	WithContext(ctx context.Context) Database
	Preload(query string, args ...interface{}) Database
	Model(value interface{}) Database
	Omit(columns ...string) Database
	Select(columns string, args ...interface{}) Database
	Order(order interface{}, reorder ...bool) Database
	Limit(limit interface{}, offset ...interface{}) Database
	PreloadWithError(query string, errorMapping func(error) error) Database
}

type EmailService interface {
	SendPasswordChangedEmail(email, name string) error
	SendEmail(to, subject, body string) error
}

type EventBus interface {
	Publish(event interface{})
	Subscribe(eventType string, handler interface{})
}
