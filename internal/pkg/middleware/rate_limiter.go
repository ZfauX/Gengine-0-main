package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

type RateLimiterStore interface {
	Allow(key string) bool
	Stop()
}

type inMemoryShard struct {
	mu       sync.Mutex
	visitors map[string]*visitor
}

const shardCount = 16

type inMemoryStore struct {
	shards [shardCount]*inMemoryShard
	window time.Duration
	limit  int
	stopCh chan struct{}
	once   sync.Once
}

type visitor struct {
	lastSeen time.Time
	count    int
}

func newInMemoryStore(window time.Duration, limit int) *inMemoryStore {
	s := &inMemoryStore{
		window: window,
		limit:  limit,
		stopCh: make(chan struct{}),
	}
	for i := 0; i < shardCount; i++ {
		s.shards[i] = &inMemoryShard{
			visitors: make(map[string]*visitor),
		}
	}
	return s
}

func (s *inMemoryStore) Allow(key string) bool {
	s.once.Do(func() {
		go s.cleanup()
	})

	shard := s.getShard(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	v, exists := shard.visitors[key]
	now := time.Now()

	if !exists || now.Sub(v.lastSeen) > s.window {
		shard.visitors[key] = &visitor{lastSeen: now, count: 1}
		return true
	}

	if v.count >= s.limit {
		return false
	}

	v.lastSeen = now
	v.count++

	return true
}

func (s *inMemoryStore) Stop() {
	close(s.stopCh)
}

func (s *inMemoryStore) cleanup() {
	interval := time.Minute
	if s.window > 0 && s.window/4 < interval {
		interval = s.window / 4
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			for _, shard := range s.shards {
				shard.mu.Lock()
				now := time.Now()
				for key, v := range shard.visitors {
					if now.Sub(v.lastSeen) > s.window {
						delete(shard.visitors, key)
					}
				}
				shard.mu.Unlock()
			}
		}
	}
}

func (s *inMemoryStore) getShard(key string) *inMemoryShard {
	var h uint32
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return s.shards[h%shardCount]
}

type valkeyStore struct {
	client *redis.Client
	window time.Duration
	limit  int
}

func newValkeyStore(client *redis.Client, window time.Duration, limit int) *valkeyStore {
	return &valkeyStore{client: client, window: window, limit: limit}
}

func (s *valkeyStore) Allow(key string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	count, err := s.client.Incr(ctx, key).Result()
	if err != nil {
		log.Error().Err(err).Str("key", key).Msg("valkey: Allow check failed, denying request")
		return false
	}
	if count == 1 {
		s.client.Expire(ctx, key, s.window)
	}
	return count <= int64(s.limit)
}

func (s *valkeyStore) Stop() {}

type RateLimiter struct {
	store RateLimiterStore
}

func NewRateLimiter(window time.Duration, limit int) *RateLimiter {
	return &RateLimiter{store: newInMemoryStore(window, limit)}
}

func NewValkeyRateLimiter(client *redis.Client, window time.Duration, limit int) *RateLimiter {
	return &RateLimiter{store: newValkeyStore(client, window, limit)}
}

func (rl *RateLimiter) Allow(key string) bool {
	return rl.store.Allow(key)
}

func (rl *RateLimiter) Stop() {
	rl.store.Stop()
}

func respondRateLimitError(c *gin.Context, message string) {
	if strings.Contains(c.GetHeader("Accept"), "text/html") {
		c.HTML(http.StatusTooManyRequests, "errors-429.html", gin.H{"Error": message})
		c.Abort()
		return
	}
	c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": message})
}

var globalRateLimiter *RateLimiter

func InitGlobalRateLimiter(window time.Duration, limit int) {
	globalRateLimiter = NewRateLimiter(window, limit)
}

func InitGlobalRateLimiterWithValkey(client *redis.Client, window time.Duration, limit int) {
	globalRateLimiter = NewValkeyRateLimiter(client, window, limit)
}

func StopGlobalRateLimiter() {
	if globalRateLimiter != nil {
		globalRateLimiter.Stop()
	}
}

func GlobalRateLimit(window time.Duration, limit int) gin.HandlerFunc {
	rl := globalRateLimiter
	if rl == nil {
		rl = NewRateLimiter(window, limit)
	}
	return func(c *gin.Context) {
		ip := c.ClientIP()
		if !rl.Allow("global:" + ip) {
			respondRateLimitError(c, ErrRateLimitGlobal)
			return
		}
		c.Next()
	}
}

var loginRateLimiter *RateLimiter

func InitLoginRateLimiter(window time.Duration, limit int) {
	loginRateLimiter = NewRateLimiter(window, limit)
}

func InitLoginRateLimiterWithValkey(client *redis.Client, window time.Duration, limit int) {
	loginRateLimiter = NewValkeyRateLimiter(client, window, limit)
}

func StopLoginRateLimiter() {
	if loginRateLimiter != nil {
		loginRateLimiter.Stop()
	}
}

func LoginRateLimit(window time.Duration, limit int) gin.HandlerFunc {
	rl := loginRateLimiter
	if rl == nil {
		rl = NewRateLimiter(window, limit)
	}
	return func(c *gin.Context) {
		ip := c.ClientIP()
		if !rl.Allow("login:" + ip) {
			respondRateLimitError(c, ErrRateLimitLogin)
			return
		}
		c.Next()
	}
}

var registrationRateLimiter *RateLimiter

func InitRegistrationRateLimiter(window time.Duration, limit int) {
	registrationRateLimiter = NewRateLimiter(window, limit)
}

func InitRegistrationRateLimiterWithValkey(client *redis.Client, window time.Duration, limit int) {
	registrationRateLimiter = NewValkeyRateLimiter(client, window, limit)
}

func StopRegistrationRateLimiter() {
	if registrationRateLimiter != nil {
		registrationRateLimiter.Stop()
	}
}

func RegistrationRateLimit(window time.Duration, limit int) gin.HandlerFunc {
	rl := registrationRateLimiter
	if rl == nil {
		rl = NewRateLimiter(window, limit)
	}
	return func(c *gin.Context) {
		ip := c.ClientIP()
		if !rl.Allow("register:" + ip) {
			respondRateLimitError(c, ErrRateLimitRegister)
			return
		}
		c.Next()
	}
}

var codeSubmissionRateLimiter *RateLimiter

func InitCodeSubmissionRateLimiter(window time.Duration, limit int) {
	codeSubmissionRateLimiter = NewRateLimiter(window, limit)
}

func InitCodeSubmissionRateLimiterWithValkey(client *redis.Client, window time.Duration, limit int) {
	codeSubmissionRateLimiter = NewValkeyRateLimiter(client, window, limit)
}

func StopCodeSubmissionRateLimiter() {
	if codeSubmissionRateLimiter != nil {
		codeSubmissionRateLimiter.Stop()
	}
}

func CodeSubmissionRateLimit(window time.Duration, limit int) gin.HandlerFunc {
	rl := codeSubmissionRateLimiter
	if rl == nil {
		rl = NewRateLimiter(window, limit)
	}
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		if userID == 0 {
			c.Next()
			return
		}
		key := fmt.Sprintf("code:%d", userID)
		if !rl.Allow(key) {
			respondRateLimitError(c, ErrRateLimitCode)
			return
		}
		c.Next()
	}
}

var sseRateLimiter *RateLimiter

func InitSSERateLimiter(window time.Duration, limit int) {
	sseRateLimiter = NewRateLimiter(window, limit)
}

func InitSSERateLimiterWithValkey(client *redis.Client, window time.Duration, limit int) {
	sseRateLimiter = NewValkeyRateLimiter(client, window, limit)
}

func StopSSERateLimiter() {
	if sseRateLimiter != nil {
		sseRateLimiter.Stop()
	}
}

func SSERateLimit(window time.Duration, limit int) gin.HandlerFunc {
	rl := sseRateLimiter
	if rl == nil {
		rl = NewRateLimiter(window, limit)
	}
	return func(c *gin.Context) {
		key := fmt.Sprintf("sse:%d", c.GetUint("userID"))
		if !rl.Allow(key) {
			respondRateLimitError(c, ErrRateLimitSSE)
			return
		}
		c.Next()
	}
}

var apiRateLimiter *RateLimiter

func InitAPIRateLimiter(window time.Duration, limit int) {
	apiRateLimiter = NewRateLimiter(window, limit)
}

func InitAPIRateLimiterWithValkey(client *redis.Client, window time.Duration, limit int) {
	apiRateLimiter = NewValkeyRateLimiter(client, window, limit)
}

func StopAPIRateLimiter() {
	if apiRateLimiter != nil {
		apiRateLimiter.Stop()
	}
}

func APIRateLimit(window time.Duration, limit int) gin.HandlerFunc {
	rl := apiRateLimiter
	if rl == nil {
		rl = NewRateLimiter(window, limit)
	}
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		if userID == 0 {
			c.Next()
			return
		}
		key := fmt.Sprintf("api:%d", userID)
		if !rl.Allow(key) {
			respondRateLimitError(c, ErrRateLimitGlobal)
			return
		}
		c.Next()
	}
}
