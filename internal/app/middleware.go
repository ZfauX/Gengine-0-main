// Package app содержит middleware уровня приложения.
package app

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
)

// prometheus метрики
var (
	HTTPRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Общее количество HTTP-запросов",
		},
		[]string{"method", "path", "status"},
	)
	HTTPRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Длительность HTTP-запросов",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)
)

func init() {
	prometheus.MustRegister(HTTPRequestsTotal, HTTPRequestDuration)
}

// LoggerMiddleware логирует запросы с помощью zerolog и обновляет Prometheus метрики.
func LoggerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery
		if raw != "" {
			path = path + "?" + raw
		}

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()
		method := c.Request.Method

		log.Info().
			Int("status", status).
			Str("method", method).
			Str("path", path).
			Dur("latency", latency).
			Str("ip", c.ClientIP()).
			Msg("HTTP запрос")

		HTTPRequestsTotal.WithLabelValues(method, c.Request.URL.Path, fmt.Sprintf("%d", status)).Inc()
		HTTPRequestDuration.WithLabelValues(method, c.Request.URL.Path).Observe(latency.Seconds())
	}
}
