// Package config provides application constants and configuration values.
package config

import "time"

const (
	SentryFlushTimeout          = 2 * time.Second
	DBRetryInitialDelay         = 2 * time.Second
	DBMaxRetryAttempts          = 5
	RateLimitWindow             = 1 * time.Minute
	GlobalRateLimit             = 100
	LoginRateLimit              = 5
	RegistrationRateLimit       = 3
	CodeSubmissionRateLimit     = 10
	SSERateLimit                = 10
	APIRateLimit                = 60
	EmailQueueWorkers           = 5
	EmailQueueInterval          = 10 * time.Second
	EmailQueueBatchSize         = 10
	CacheDefaultTTL             = 10 * time.Minute
	CacheCleanupInterval        = 5 * time.Minute
	PoolMonitorInterval         = 1 * time.Minute
	RefreshTokenCleanupInterval = 1 * time.Hour
	ServerReadTimeout           = 15 * time.Second
	ServerWriteTimeout          = 30 * time.Second
	ServerIdleTimeout           = 120 * time.Second
	ShutdownTimeout             = 45 * time.Second
)
