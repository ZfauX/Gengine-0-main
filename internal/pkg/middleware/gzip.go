// internal/pkg/middleware/gzip.go
package middleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// gzipResponseWriter реализует io.Writer поверх ResponseWriter для потокового сжатия.
type gzipResponseWriter struct {
	gin.ResponseWriter
	writer  *gzip.Writer
	written bool
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")
		w.written = true
	}
	return w.writer.Write(b)
}

func (w *gzipResponseWriter) WriteString(s string) (int, error) {
	if !w.written {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")
		w.written = true
	}
	return io.WriteString(w.writer, s)
}

// Flush сбрасывает gzip-буфер и затем базовый writer.
// Критично для SSE/streaming ответов — иначе данные застревают в gzip-буфере,
// и клиент получает ERR_INCOMPLETE_CHUNKED_ENCODING при закрытии соединения.
func (w *gzipResponseWriter) Flush() {
	if w.writer != nil {
		_ = w.writer.Flush()
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func GzipMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !strings.Contains(c.GetHeader("Accept-Encoding"), "gzip") {
			c.Next()
			return
		}

		if c.Request.URL.Path == "/metrics" ||
			strings.HasPrefix(c.Request.URL.Path, "/ws") ||
			// Исключаем WebSocket/SSE-маршруты (после hijack нельзя писать gzip trailer),
			// включая /games/:id/monitor/ws, /games/:id/chat/ws, /games/:id/logs/ws,
			// SSE-потоки монитора и геймплея (/game/:id/sse, /game/sse/:id).
			strings.Contains(c.Request.URL.Path, "/ws") ||
			strings.Contains(c.Request.URL.Path, "/monitor") ||
			strings.Contains(c.Request.URL.Path, "/stream") ||
			strings.Contains(c.Request.URL.Path, "/sse") ||
			strings.HasPrefix(c.Request.URL.Path, "/static/") ||
			strings.HasPrefix(c.Request.URL.Path, "/uploads/") {
			c.Next()
			return
		}

		// Потоковое сжатие: не буферизируем весь ответ в памяти
		gz := gzip.NewWriter(c.Writer)
		// Закрываем в defer — даже при панике хендлера gzip-поток завершится корректно,
		// иначе Recovery запишет 500 через незакрытый gzip-writer (битый ответ).
		defer func() {
			if err := gz.Close(); err != nil {
				log.Debug().Err(err).Msg("GzipMiddleware: gzip close failed")
			}
		}()

		gzWriter := &gzipResponseWriter{
			ResponseWriter: c.Writer,
			writer:         gz,
		}
		c.Writer = gzWriter

		c.Next()
	}
}
