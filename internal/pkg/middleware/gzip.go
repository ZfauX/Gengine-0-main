// internal/pkg/middleware/gzip.go
package middleware

import (
	"compress/gzip"
	"io"
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

func GzipMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !strings.Contains(c.GetHeader("Accept-Encoding"), "gzip") {
			c.Next()
			return
		}

		if c.Request.URL.Path == "/metrics" {
			c.Next()
			return
		}

		// Потоковое сжатие: не буферизируем весь ответ в памяти
		gz := gzip.NewWriter(c.Writer)
		gzWriter := &gzipResponseWriter{
			ResponseWriter: c.Writer,
			writer:         gz,
		}
		c.Writer = gzWriter

		c.Next()

		// Закрываем gzip-writer (записывает trailer и flush)
		if err := gz.Close(); err != nil {
			log.Debug().Err(err).Msg("GzipMiddleware: gzip close failed")
		}
	}
}
