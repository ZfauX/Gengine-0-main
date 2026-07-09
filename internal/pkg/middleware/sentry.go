// internal/pkg/middleware/sentry.go
package middleware

import (
	"net/http"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
)

// SentryMiddleware интегрирует Sentry для отслеживания ошибок и паник.
// Вызывать после инициализации sentry.Init().
func SentryMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Создаём hub для текущего запроса
		hub := sentry.CurrentHub().Clone()
		scope := hub.Scope()

		// Добавляем контекст запроса
		scope.SetRequest(c.Request)
		scope.SetTag("path", c.Request.URL.Path)
		scope.SetTag("method", c.Request.Method)

		// Сохраняем hub в контексте для использования в хендлерах
		c.Set("sentryHub", hub)

		start := time.Now()

		// Перехватываем паники
		defer func() {
			if err := recover(); err != nil {
				// Логируем панику в Sentry
				hub.RecoverWithContext(c.Request.Context(), err)
				hub.Flush(2 * time.Second)
				panic(err)
			}
		}()

		c.Next()

		// Логируем HTTP-статусы 5xx
		status := c.Writer.Status()
		if status >= http.StatusInternalServerError {
			hub.CaptureMessage("HTTP " + string(rune(status)))
		}

		// Замеряем длительность
		duration := time.Since(start)
		scope.SetTag("duration", duration.String())
	}
}

// GetSentryHub возвращает Sentry hub из контекста.
func GetSentryHub(c *gin.Context) *sentry.Hub {
	if hub, exists := c.Get("sentryHub"); exists {
		if h, ok := hub.(*sentry.Hub); ok {
			return h
		}
	}
	return sentry.CurrentHub()
}
