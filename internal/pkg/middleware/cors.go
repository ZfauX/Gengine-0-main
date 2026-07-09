// internal/pkg/middleware/cors.go
package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// CORSMiddleware добавляет CORS-заголовки для API-эндпоинтов.
// Позволяет кросс-доменные запросы с указанных origins.
func CORSMiddleware(allowedOrigins []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		allowed := false

		for _, ao := range allowedOrigins {
			if ao == "*" || strings.EqualFold(origin, ao) {
				allowed = true
				break
			}
		}

		if allowed {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization, X-CSRF-Token")
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Max-Age", "86400")
		}

		// Обработка preflight-запросов
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}
