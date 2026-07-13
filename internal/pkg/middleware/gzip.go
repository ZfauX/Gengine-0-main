// internal/pkg/middleware/gzip.go
package middleware

import (
	"bufio"
	"compress/gzip"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

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

		// Не сжимаем бинарные файлы и уже сжатые типы
		ct := c.Writer.Header().Get("Content-Type")
		if strings.HasPrefix(ct, "image/") ||
			strings.HasPrefix(ct, "application/zip") ||
			strings.HasPrefix(ct, "application/pdf") ||
			strings.HasPrefix(ct, "video/") ||
			strings.HasPrefix(ct, "audio/") {
			c.Next()
			return
		}

		gz := gzip.NewWriter(c.Writer)
		defer func() { _ = gz.Close() }()

		c.Writer = &gzipResponseWriter{
			ResponseWriter: c.Writer,
			Writer:         gz,
		}
		c.Header("Content-Encoding", "gzip")
		c.Header("Vary", "Accept-Encoding")
		c.Next()
	}
}

type gzipResponseWriter struct {
	gin.ResponseWriter
	io.Writer
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	return w.Writer.Write(b)
}

func (w *gzipResponseWriter) WriteString(s string) (int, error) {
	return w.Writer.Write([]byte(s))
}

func (w *gzipResponseWriter) Flush() {
	if flusher, ok := w.Writer.(interface{ Flush() error }); ok {
		_ = flusher.Flush()
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *gzipResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}
