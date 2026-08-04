// Package middleware предоставляет middleware для контекста с таймаутами.
package middleware

import (
	"context"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// ContextTimeout добавляет context.Context с таймаутом в gin.Context.
// По умолчанию — 30 секунд для обработки запроса.
// Исключает WebSocket-маршруты (/ws, /monitor) из таймаута.
func ContextTimeout(timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Исключаем WebSocket и monitor-маршруты из таймаута
		// (после upgrade соединение живёт дольше таймаута HTTP-запроса)
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/ws") || strings.Contains(path, "/ws") ||
			strings.Contains(path, "/monitor") || strings.Contains(path, "/stream") ||
			strings.Contains(path, "/sse") {
			c.Next()
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()

		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
