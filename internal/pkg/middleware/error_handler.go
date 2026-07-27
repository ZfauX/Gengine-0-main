// internal/pkg/middleware/error_handler.go
package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"gengine-0/internal/pkg/errors"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// renderErrorHTML отправляет простую HTML-страницу ошибки (без шаблонизатора,
// чтобы избежать циклического импорта с пакетом render).
func renderErrorHTML(c *gin.Context, status int, title, message string) {
	c.Status(status)
	c.Header("Content-Type", "text/html; charset=utf-8")
	body := fmt.Sprintf(`<!DOCTYPE html><html lang="ru"><head><meta charset="UTF-8"><title>%s</title>
<style>body{font-family:system-ui,sans-serif;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0;background:#f3f4f6}
.card{background:#fff;border-radius:1rem;padding:3rem;text-align:center;max-width:400px;box-shadow:0 10px 25px rgba(0,0,0,0.1)}
h1{color:#111827;font-size:1.5rem;margin-bottom:.5rem}p{color:#6b7280;margin-bottom:1.5rem}
a{display:inline-block;padding:.75rem 1.5rem;background:#2563eb;color:#fff;border-radius:.5rem;text-decoration:none;font-weight:500}
a:hover{background:#1d4ed8}</style></head><body><div class="card"><h1>%s</h1><p>%s</p><a href="/">На главную</a></div></body></html>`, title, title, message)
	_, _ = c.Writer.Write([]byte(body))
	c.Abort()
}

// ErrorHandler обрабатывает паники и возвращает HTML для браузеров или JSON для API-клиентов.
func ErrorHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				log.Error().Interface("panic", r).Msg("Panic recovered")
				if isHTMLRequest(c) {
					renderErrorHTML(c, http.StatusInternalServerError, "500 — Ошибка сервера", "Произошла внутренняя ошибка. Пожалуйста, попробуйте позже.")
				} else {
					c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
						"error": ErrInternalServer.Error(),
						"code":  "internal_error",
					})
				}
			}
		}()

		c.Next()

		if len(c.Errors) > 0 {
			if c.Writer.Written() {
				log.Warn().Err(c.Errors.Last().Err).Msg("ErrorHandler: headers already sent, skipping")
				return
			}
			err := c.Errors.Last().Err
			if appErr, ok := err.(*errors.AppError); ok {
				if isHTMLRequest(c) {
					renderErrorHTML(c, appErr.HTTPStatus, http.StatusText(appErr.HTTPStatus), appErr.Message)
				} else {
					c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
						"error":   appErr.Message,
						"code":    appErr.Code,
						"details": appErr.Details,
					})
				}
				return
			}
			log.Error().Err(err).Msg("Unhandled error")
			if isHTMLRequest(c) {
				renderErrorHTML(c, http.StatusInternalServerError, "500 — Ошибка сервера", "Произошла внутренняя ошибка. Пожалуйста, попробуйте позже.")
			} else {
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"error": ErrInternalServer.Error(),
					"code":  "internal_error",
				})
			}
		}
	}
}

// isHTMLRequest проверяет Accept-заголовок запроса на наличие text/html.
func isHTMLRequest(c *gin.Context) bool {
	accept := c.GetHeader("Accept")
	return strings.Contains(accept, "text/html")
}
