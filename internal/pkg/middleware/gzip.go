// internal/pkg/middleware/gzip.go
package middleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// gzipWriterPool (H3, PASS-22): переиспользуем *gzip.Writer вместо создания
// flate-компрессора (~64KB истории/hash-таблиц) на каждый ответ. Пул
// потокобезопасен; Reset сбрасывает состояние под новый ResponseWriter.
var gzipWriterPool = sync.Pool{
	New: func() any {
		return gzip.NewWriter(io.Discard)
	},
}

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

// WriteHeader (DEEP-REVIEW PASS-2): ставим Content-Encoding ДО вызова базового
// WriteHeader и удаляем Content-Length. Раньше http.ServeContent (r.Static)
// записывал Content-Length несжатого файла, а gzip-запись давала меньше байт —
// Go-сервер обрывал соединение на недописанный Content-Length (клиент получал
// corrupt/truncated JS/CSS). Это была регрессия pass-1 (gzip для /static/).
func (w *gzipResponseWriter) WriteHeader(code int) {
	if !w.written {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")
		w.Header().Del("Content-Length")
		w.written = true
	}
	w.ResponseWriter.WriteHeader(code)
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
			strings.HasPrefix(c.Request.URL.Path, "/uploads/") {
			c.Next()
			return
		}

		// DEEP-REVIEW P6 (pass 46): /static/ больше не исключается целиком —
		// JS/CSS (4+3 файла) сжимаются gzip (экономия ~60-70% трафика).
		// Уже-сжатые форматы (изображения, шрифты, медиа, wasm) пропускаем,
		// чтобы не тратить CPU на бесполезное сжатие.
		if strings.HasPrefix(c.Request.URL.Path, "/static/") {
			if isAlreadyCompressedExt(c.Request.URL.Path) {
				c.Next()
				return
			}
		}

		// Потоковое сжатие: не буферизируем весь ответ в памяти.
		// H3 (PASS-22): берём writer из пула (не аллоцируем flate на каждый ответ).
		gzObj := gzipWriterPool.Get()
		gz, ok := gzObj.(*gzip.Writer)
		if !ok {
			gz = gzip.NewWriter(c.Writer)
		}
		gz.Reset(c.Writer)
		// Закрываем в defer — даже при панике хендлера gzip-поток завершится корректно,
		// иначе Recovery запишет 500 через незакрытый gzip-writer (битый ответ).
		defer func() {
			if err := gz.Close(); err != nil {
				log.Debug().Err(err).Msg("GzipMiddleware: gzip close failed")
			}
			gzipWriterPool.Put(gz)
		}()

		gzWriter := &gzipResponseWriter{
			ResponseWriter: c.Writer,
			writer:         gz,
		}
		c.Writer = gzWriter

		c.Next()
	}
}

// isAlreadyCompressedExt возвращает true для форматов, которые не стоит
// сжимать gzip (уже сжаты или плохо сжимаются) — изображения, шрифты,
// медиа, WebAssembly (DEEP-REVIEW P6, pass 46).
func isAlreadyCompressedExt(path string) bool {
	ext := strings.ToLower(path[strings.LastIndex(path, ".")+1:])
	switch ext {
	case "png", "jpg", "jpeg", "gif", "webp", "svg", "ico",
		"woff", "woff2", "ttf", "otf", "eot",
		"mp4", "webm", "mp3", "ogg", "wav", "zip", "gz",
		"wasm":
		return true
	default:
		return false
	}
}
