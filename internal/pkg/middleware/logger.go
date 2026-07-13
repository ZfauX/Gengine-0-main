// internal/pkg/middleware/logger.go
package middleware

import (
	"strconv"
	"strings"
	"time"

	"gengine-0/internal/pkg/metrics"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// sensitiveParams — параметры query, которые должны маскироваться в логах.
var sensitiveParams = map[string]bool{
	"token":         true,
	"refresh_token": true,
	"state":         true,
	"code":          true,
	"password":      true,
}

// maskQuery маскирует чувствительные параметры в query string.
func maskQuery(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	parts := strings.Split(rawQuery, "&")
	for i, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 && sensitiveParams[strings.ToLower(kv[0])] {
			parts[i] = kv[0] + "=***"
		}
	}
	return strings.Join(parts, "&")
}

// LoggerMiddleware логирует HTTP-запросы и обновляет Prometheus метрики.
func LoggerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := maskQuery(c.Request.URL.RawQuery)
		if raw != "" {
			path = path + "?" + raw
		}

		// Читаем размер запроса для метрик
		requestSize := float64(c.Request.ContentLength)

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()
		method := c.Request.Method
		statusStr := strconv.Itoa(status)

		log.Info().
			Int("status", status).
			Str("method", method).
			Str("path", path).
			Dur("latency", latency).
			Str("ip", c.ClientIP()).
			Int("size", c.Writer.Size()).
			Msg("HTTP запрос")

		// Обновляем метрики Prometheus
		metrics.RequestsTotal.WithLabelValues(method, c.Request.URL.Path, statusStr).Inc()
		metrics.RequestDuration.WithLabelValues(method, c.Request.URL.Path).Observe(latency.Seconds())
		if requestSize > 0 {
			metrics.RequestSize.WithLabelValues(method, c.Request.URL.Path).Observe(requestSize)
		}
		if c.Writer.Size() > 0 {
			metrics.ResponseSize.WithLabelValues(method, c.Request.URL.Path).Observe(float64(c.Writer.Size()))
		}
	}
}
