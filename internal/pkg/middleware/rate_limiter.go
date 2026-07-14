// internal/pkg/middleware/rate_limiter.go
package middleware

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// visitor хранит временную метку последнего запроса и счётчик обращений в текущем окне.
type visitor struct {
	lastSeen time.Time
	count    int
}

// RateLimiter — общий in‑memory rate limiter.
type RateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	window   time.Duration
	limit    int
	stopCh   chan struct{}
}

// NewRateLimiter создаёт новый лимитер. Параметр window задаёт временное окно (например, 1 минута),
// limit — максимальное количество запросов в этом окне.
func NewRateLimiter(window time.Duration, limit int) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*visitor),
		window:   window,
		limit:    limit,
		stopCh:   make(chan struct{}),
	}
	go rl.cleanup()
	return rl
}

// Stop останавливает фоновую горутину очистки.
func (rl *RateLimiter) Stop() {
	close(rl.stopCh)
}

// Allow возвращает true, если запрос с ключом key разрешён.
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[key]
	now := time.Now()

	if !exists || now.Sub(v.lastSeen) > rl.window {
		rl.visitors[key] = &visitor{lastSeen: now, count: 1}
		return true
	}

	if v.count >= rl.limit {
		return false
	}

	v.lastSeen = now
	v.count++

	return true
}

// cleanup удаляет записи, которые не обновлялись дольше окна.
func (rl *RateLimiter) cleanup() {
	// Интервал очистки — не чаще 1 раза в минуту и не реже window/4
	interval := time.Minute
	if rl.window > 0 && rl.window/4 < interval {
		interval = rl.window / 4
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-rl.stopCh:
			return
		case <-ticker.C:
			rl.mu.Lock()
			now := time.Now()
			for key, v := range rl.visitors {
				if now.Sub(v.lastSeen) > rl.window {
					delete(rl.visitors, key)
				}
			}
			rl.mu.Unlock()
		}
	}
}

// GlobalRateLimit — middleware, ограничивающий общее количество запросов с IP.
// Использует глобальный singleton, инициализированный через InitGlobalRateLimiter.
var globalRateLimiter *RateLimiter

func InitGlobalRateLimiter(window time.Duration, limit int) {
	globalRateLimiter = NewRateLimiter(window, limit)
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
		if !rl.Allow(ip) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "слишком много запросов"})
			return
		}
		c.Next()
	}
}

// LoginRateLimit — middleware, ограничивающий количество попыток входа с IP.
var loginRateLimiter *RateLimiter

func InitLoginRateLimiter(window time.Duration, limit int) {
	loginRateLimiter = NewRateLimiter(window, limit)
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
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "слишком много попыток входа, попробуйте позже"})
			return
		}
		c.Next()
	}
}

// RegistrationRateLimit — middleware, ограничивающий количество попыток регистрации с IP.
var registrationRateLimiter *RateLimiter

func InitRegistrationRateLimiter(window time.Duration, limit int) {
	registrationRateLimiter = NewRateLimiter(window, limit)
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
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "слишком много попыток регистрации, попробуйте позже"})
			return
		}
		c.Next()
	}
}

// CodeSubmissionRateLimit — middleware, ограничивающий частоту ввода кодов пользователем.
var codeSubmissionRateLimiter *RateLimiter

func InitCodeSubmissionRateLimiter(window time.Duration, limit int) {
	codeSubmissionRateLimiter = NewRateLimiter(window, limit)
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
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "слишком частый ввод кодов"})
			return
		}
		c.Next()
	}
}
