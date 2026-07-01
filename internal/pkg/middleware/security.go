// internal/pkg/middleware/security.go
package middleware

import (
	"crypto/rand"
	"encoding/base64"

	"github.com/gin-gonic/gin"
)

// SecurityHeadersMiddleware добавляет базовые защитные заголовки ко всем ответам.
// Генерирует nonce для инлайн-скриптов и стилей.
func SecurityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Генерируем nonce (16 байт случайных данных, base64)
		nonceBytes := make([]byte, 16)
		_, _ = rand.Read(nonceBytes)
		nonce := base64.StdEncoding.EncodeToString(nonceBytes)

		// Сохраняем nonce в контексте для использования в шаблонах
		c.Set("csp_nonce", nonce)

		// Формируем CSP с nonce
		csp := "default-src 'self'; " +
			"script-src 'self' 'nonce-" + nonce + "' https://cdn.jsdelivr.net; " +
			"style-src 'self' 'nonce-" + nonce + "' https://cdn.jsdelivr.net https://unpkg.com; " +
			"img-src 'self' data: https:; " +
			"connect-src 'self' ws: wss:; " +
			"frame-src 'self' https://www.youtube.com https://player.vimeo.com https://rutube.ru;"

		c.Header("Content-Security-Policy", csp)
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		c.Header("Permissions-Policy", "geolocation=(), microphone=(), camera=(), payment=(), usb=(), fullscreen=(self), sync-xhr=(self), accelerometer=(), gyroscope=(), magnetometer=()")

		c.Next()
	}
}

// GetCSPNonce возвращает nonce из контекста для использования в шаблонах.
func GetCSPNonce(c *gin.Context) string {
	if nonce, exists := c.Get("csp_nonce"); exists {
		if s, ok := nonce.(string); ok {
			return s
		}
	}
	return ""
}
