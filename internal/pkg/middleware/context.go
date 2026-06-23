// Package middleware предоставляет middleware для контекста с таймаутами.
package middleware

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
)

// ContextTimeout добавляет context.Context с таймаутом в gin.Context.
// По умолчанию — 30 секунд для обработки запроса.
func ContextTimeout(timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()

		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
