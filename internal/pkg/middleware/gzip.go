// internal/pkg/middleware/gzip.go
package middleware

import (
	"bytes"
	"compress/gzip"
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

		// Перехватываем body через custom writer
		sw := &switchWriter{ResponseWriter: c.Writer, buf: &bytes.Buffer{}}
		c.Writer = sw

		c.Next()

		// Не сжимаем бинарные файлы и уже сжатые типы
		ct := sw.Header().Get("Content-Type")
		if strings.HasPrefix(ct, "image/") ||
			strings.HasPrefix(ct, "application/zip") ||
			strings.HasPrefix(ct, "application/pdf") ||
			strings.HasPrefix(ct, "video/") ||
			strings.HasPrefix(ct, "audio/") {
			// Копируем uncompressed body back
			sw.ResponseWriter.Write(sw.buf.Bytes())
			return
		}

		// Сжимаем и записываем
		if sw.buf.Len() > 0 {
			c.Header("Content-Encoding", "gzip")
			c.Header("Vary", "Accept-Encoding")
			gzWriter := gzip.NewWriter(sw.ResponseWriter)
			_, _ = gzWriter.Write(sw.buf.Bytes())
			_ = gzWriter.Close()
		}
	}
}

type switchWriter struct {
	gin.ResponseWriter
	buf *bytes.Buffer
}

func (w *switchWriter) Write(b []byte) (int, error) {
	w.buf.Write(b)
	return len(b), nil
}

func (w *switchWriter) WriteString(s string) (int, error) {
	w.buf.WriteString(s)
	return len(s), nil
}
