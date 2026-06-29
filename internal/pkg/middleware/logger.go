// internal/pkg/middleware/logger.go
package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// LoggerMiddleware логирует HTTP-запросы: метод, путь, статус, длительность, IP, User-Agent.
// Тело запроса не логируется для защиты конфиденциальных данных.
func LoggerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		// Пропускаем логирование для эндпоинтов мониторинга (опционально)
		// if path == "/metrics" || path == "/health" {
		// 	c.Next()
		// 	return
		// }

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()
		clientIP := c.ClientIP()
		method := c.Request.Method
		userAgent := c.Request.UserAgent()

		event := log.Info().
			Str("method", method).
			Str("path", path).
			Int("status", status).
			Dur("latency", latency).
			Str("ip", clientIP).
			Str("user_agent", userAgent)

		if raw != "" {
			event.Str("query", raw)
		}

		// Для ошибок можно повысить уровень логирования
		if status >= 500 {
			event = log.Error()
		} else if status >= 400 {
			event = log.Warn()
		}

		event.Msg("HTTP request")
	}
}
