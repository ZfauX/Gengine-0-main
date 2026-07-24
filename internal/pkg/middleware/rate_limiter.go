package middleware

import (
	"context"
	"fmt"
	"hash/fnv"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

type RateLimitResult struct {
	Allowed   bool
	Limit     int
	Remaining int
	ResetUnix int64
}

type RateLimiterStore interface {
	Allow(key string) RateLimitResult
	Stop()
}

type inMemoryShard struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	dirty    int32
}

const shardCount = 16

type inMemoryStore struct {
	shards      [shardCount]*inMemoryShard
	window      time.Duration
	limit       int
	stopCh      chan struct{}
	cleanupOnce sync.Once
	stopOnce    sync.Once
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

func (s *inMemoryStore) Allow(key string) RateLimitResult {
	s.cleanupOnce.Do(func() {
		go s.cleanupLoop()
	})

	shard := s.getShard(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	v, exists := shard.visitors[key]
	now := time.Now()

	if !exists || now.Sub(v.lastSeen) > s.window {
		shard.visitors[key] = &visitor{lastSeen: now, count: 1}
		atomic.StoreInt32(&shard.dirty, 1)
		return RateLimitResult{Allowed: true, Limit: s.limit, Remaining: s.limit - 1, ResetUnix: now.Add(s.window).Unix()}
	}

	if v.count >= s.limit {
		return RateLimitResult{Allowed: false, Limit: s.limit, Remaining: 0, ResetUnix: v.lastSeen.Add(s.window).Unix()}
	}

	v.lastSeen = now
	v.count++

	return RateLimitResult{Allowed: true, Limit: s.limit, Remaining: s.limit - v.count, ResetUnix: v.lastSeen.Add(s.window).Unix()}
}

func (s *inMemoryStore) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
}

func (s *inMemoryStore) cleanupLoop() {
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
				if atomic.LoadInt32(&shard.dirty) == 0 {
					continue
				}
				shard.mu.Lock()
				now := time.Now()
				for key, v := range shard.visitors {
					if now.Sub(v.lastSeen) > s.window {
						delete(shard.visitors, key)
					}
				}
				if len(shard.visitors) == 0 {
					atomic.StoreInt32(&shard.dirty, 0)
				}
				shard.mu.Unlock()
			}
		}
	}
}

func (s *inMemoryStore) getShard(key string) *inMemoryShard {
	f := fnv.New32a()
	f.Write([]byte(key))
	return s.shards[f.Sum32()%shardCount]
}

var rateLimitLua = redis.NewScript(`
	local key = KEYS[1]
	local limit = tonumber(ARGV[1])
	local window = tonumber(ARGV[2])

	local count = redis.call('INCR', key)
	if count == 1 then
		redis.call('EXPIRE', key, window)
	end

	if count <= limit then
		return {1, count, limit - count}
	else
		local ttl = redis.call('TTL', key)
		if ttl < 0 then ttl = 0 end
		return {0, count, 0, ttl}
	end
`)

type valkeyStore struct {
	client *redis.Client
	window time.Duration
	limit  int
}

func newValkeyStore(client *redis.Client, window time.Duration, limit int) *valkeyStore {
	return &valkeyStore{client: client, window: window, limit: limit}
}

func (s *valkeyStore) Allow(key string) RateLimitResult {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	windowSec := int64(s.window.Seconds())
	if windowSec < 1 {
		windowSec = 1
	}
	resetUnix := time.Now().Add(s.window).Unix()

	result, err := rateLimitLua.Run(ctx, s.client, []string{key}, s.limit, windowSec).Result()
	if err != nil {
		log.Error().Err(err).Str("key", key).Msg("valkey: Allow check failed, denying request")
		return RateLimitResult{Allowed: false, Limit: s.limit, Remaining: 0, ResetUnix: resetUnix}
	}

	vals, ok := result.([]any)
	if !ok || len(vals) < 3 {
		log.Error().Str("key", key).Interface("result", result).Msg("valkey: unexpected script result, denying request")
		return RateLimitResult{Allowed: false, Limit: s.limit, Remaining: 0, ResetUnix: resetUnix}
	}

	allowed, _ := vals[0].(int64)
	remaining, _ := vals[2].(int64)
	if remaining < 0 {
		remaining = 0
	}

	return RateLimitResult{
		Allowed:   allowed == 1,
		Limit:     s.limit,
		Remaining: int(remaining),
		ResetUnix: resetUnix,
	}
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

func (rl *RateLimiter) Allow(key string) RateLimitResult {
	return rl.store.Allow(key)
}

func (rl *RateLimiter) Stop() {
	rl.store.Stop()
}

func setRateLimitHeaders(c *gin.Context, result RateLimitResult) {
	c.Header("X-RateLimit-Limit", strconv.Itoa(result.Limit))
	c.Header("X-RateLimit-Remaining", strconv.Itoa(result.Remaining))
	c.Header("X-RateLimit-Reset", strconv.FormatInt(result.ResetUnix, 10))
	if !result.Allowed {
		retryAfter := int(time.Until(time.Unix(result.ResetUnix, 0)).Seconds())
		if retryAfter < 0 {
			retryAfter = 0
		}
		c.Header("Retry-After", strconv.Itoa(retryAfter))
	}
}

func respondRateLimitError(c *gin.Context, message string, result RateLimitResult) {
	setRateLimitHeaders(c, result)
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
		result := rl.Allow("global:" + ip)
		setRateLimitHeaders(c, result)
		if !result.Allowed {
			respondRateLimitError(c, ErrRateLimitGlobal, result)
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
		result := rl.Allow("login:" + ip)
		setRateLimitHeaders(c, result)
		if !result.Allowed {
			respondRateLimitError(c, ErrRateLimitLogin, result)
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
		result := rl.Allow("register:" + ip)
		setRateLimitHeaders(c, result)
		if !result.Allowed {
			respondRateLimitError(c, ErrRateLimitRegister, result)
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
		result := rl.Allow(key)
		setRateLimitHeaders(c, result)
		if !result.Allowed {
			respondRateLimitError(c, ErrRateLimitCode, result)
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
		result := rl.Allow(key)
		setRateLimitHeaders(c, result)
		if !result.Allowed {
			respondRateLimitError(c, ErrRateLimitSSE, result)
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
		result := rl.Allow(key)
		setRateLimitHeaders(c, result)
		if !result.Allowed {
			respondRateLimitError(c, ErrRateLimitGlobal, result)
			return
		}
		c.Next()
	}
}
