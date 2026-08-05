// Package config provides application constants and configuration values.
package config

import "time"

const (
	SentryFlushTimeout  = 2 * time.Second
	DBRetryInitialDelay = 2 * time.Second
	DBMaxRetryAttempts  = 5
	RateLimitWindow     = 1 * time.Minute
	// StaticAssetsVersion — версия статики, подставляемая в ?v= для версионированных
	// ассетов (layout.html через render.Page). При изменении статики обнови это
	// значение И константу ASSET_VERSION в static/sw.js (единый источник — Go).
	StaticAssetsVersion     = "20260805"
	GlobalRateLimit         = 100
	LoginRateLimit          = 5
	RegistrationRateLimit   = 3
	CodeSubmissionRateLimit = 10
	SSERateLimit            = 10
	APIRateLimit            = 60
	PasswordResetRateLimit  = 5
	EmailQueueWorkers       = 5
	EmailQueueInterval      = 10 * time.Second
	EmailQueueBatchSize     = 10
	CacheDefaultTTL         = 10 * time.Minute
	CacheCleanupInterval    = 5 * time.Minute
	// CacheMaxItems — верхняя граница in-memory LRU (P11): защита от неограниченного роста
	// памяти при большом количестве уникальных ключей (игры, рейтинги, листинги и т.п.).
	CacheMaxItems               = 10000
	PoolMonitorInterval         = 1 * time.Minute
	RefreshTokenCleanupInterval = 1 * time.Hour
	ServerReadTimeout           = 15 * time.Second
	ServerIdleTimeout           = 120 * time.Second
	ShutdownTimeout             = 45 * time.Second
)
