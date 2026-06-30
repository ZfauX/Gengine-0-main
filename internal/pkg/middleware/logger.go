// internal/pkg/middleware/logger.go
package middleware

import (
	"strconv"
	"time"

	"gengine-0/internal/pkg/metrics"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// LoggerMiddleware логирует HTTP-запросы и обновляет Prometheus метрики.
func LoggerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery
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
		metrics.RequestsTotal.WithLabelValues(method, path, statusStr).Inc()
		metrics.RequestDuration.WithLabelValues(method, path).Observe(latency.Seconds())
		if requestSize > 0 {
			metrics.RequestSize.WithLabelValues(method, path).Observe(requestSize)
		}
		if c.Writer.Size() > 0 {
			metrics.ResponseSize.WithLabelValues(method, path).Observe(float64(c.Writer.Size()))
		}
	}
}
