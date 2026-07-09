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
		v.lastSeen = now
		return false
	}

	v.lastSeen = now
	v.count++

	return true
}

// cleanup удаляет записи, которые не обновлялись дольше окна.
func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(time.Minute)
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
// Возвращает handler, который использует глобальный singleton RateLimiter.
var globalRateLimiter *RateLimiter

// InitGlobalRateLimiter инициализирует глобальный rate limiter (вызывать один раз при старте).
func InitGlobalRateLimiter(window time.Duration, limit int) {
	globalRateLimiter = NewRateLimiter(window, limit)
}

// StopGlobalRateLimiter останавливает глобальный rate limiter (вызывать при graceful shutdown).
func StopGlobalRateLimiter() {
	if globalRateLimiter != nil {
		globalRateLimiter.Stop()
	}
}

// GlobalRateLimit — middleware, ограничивающий общее количество запросов с IP.
func GlobalRateLimit(window time.Duration, limit int) gin.HandlerFunc {
	// Если глобальный лимитер уже создан, используем его; иначе создаём локальный
	if globalRateLimiter != nil {
		return func(c *gin.Context) {
			ip := c.ClientIP()
			if !globalRateLimiter.Allow(ip) {
				c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "слишком много запросов"})
				return
			}
			c.Next()
		}
	}

	rl := NewRateLimiter(window, limit)
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

// InitLoginRateLimiter инициализирует глобальный rate limiter для логина.
func InitLoginRateLimiter(window time.Duration, limit int) {
	loginRateLimiter = NewRateLimiter(window, limit)
}

// StopLoginRateLimiter останавливает глобальный rate limiter для логина.
func StopLoginRateLimiter() {
	if loginRateLimiter != nil {
		loginRateLimiter.Stop()
	}
}

// LoginRateLimit — middleware, ограничивающий количество попыток входа с IP.
func LoginRateLimit(window time.Duration, limit int) gin.HandlerFunc {
	if loginRateLimiter != nil {
		return func(c *gin.Context) {
			ip := c.ClientIP()
			if !loginRateLimiter.Allow("login:" + ip) {
				c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "слишком много попыток входа, попробуйте позже"})
				return
			}
			c.Next()
		}
	}

	rl := NewRateLimiter(window, limit)
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

// InitRegistrationRateLimiter инициализирует глобальный rate limiter для регистрации.
func InitRegistrationRateLimiter(window time.Duration, limit int) {
	registrationRateLimiter = NewRateLimiter(window, limit)
}

// StopRegistrationRateLimiter останавливает глобальный rate limiter для регистрации.
func StopRegistrationRateLimiter() {
	if registrationRateLimiter != nil {
		registrationRateLimiter.Stop()
	}
}

// RegistrationRateLimit — middleware, ограничивающий количество попыток регистрации с IP.
func RegistrationRateLimit(window time.Duration, limit int) gin.HandlerFunc {
	if registrationRateLimiter != nil {
		return func(c *gin.Context) {
			ip := c.ClientIP()
			if !registrationRateLimiter.Allow("register:" + ip) {
				c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "слишком много попыток регистрации, попробуйте позже"})
				return
			}
			c.Next()
		}
	}

	rl := NewRateLimiter(window, limit)
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
func CodeSubmissionRateLimit(window time.Duration, limit int) gin.HandlerFunc {
	rl := NewRateLimiter(window, limit)
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		if userID == 0 {
			// Если пользователь не аутентифицирован, разрешаем; реальная защита на уровне маршрута
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
