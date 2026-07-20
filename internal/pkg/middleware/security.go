// internal/pkg/middleware/security.go
package middleware

import (
	"crypto/rand"
	"encoding/base64"
	"sync"

	"github.com/gin-gonic/gin"
)

// noncePool — буфер предварительно сгенерированных nonce для снижения нагрузки на crypto/rand
var noncePool = sync.Pool{
	New: func() any {
		b := make([]byte, 16)
		return &b
	},
}

// getLeafletHash возвращает SHA-256 hash для Leaflet 1.9.4 JS.
// Вычисляется: openssl dgst -sha256 -binary leaflet.js | base64
const leafletJSHash = "'sha256-20nQCchB9co0qIjJZRGuk2/Z9VM+kNiyxNV1lvTlZBo='"

// getLeafletCSSHash возвращает SHA-256 hash для Leaflet 1.9.4 CSS.
const leafletCSSHash = "'sha256-p4NxAoJBhIIN+hmNHrzRCf9tD/miZyoHS5obTRR9BMY='"

// SecurityHeadersMiddleware добавляет базовые защитные заголовки ко всем ответам.
// Генерирует nonce для инлайн-скриптов и стилей.
func SecurityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Генерируем nonce (16 байт случайных данных, base64)
		nonceBytes := noncePool.Get().(*[]byte)
		if _, err := rand.Read(*nonceBytes); err != nil {
			// При ошибке rand.Read используем fallback-nonce (безопасность снижена, но не заблокирована)
			nonce := "fallback"
			c.Set("csp_nonce", nonce)
			setCSPHeaders(c, nonce)
			noncePool.Put(nonceBytes)
			c.Next()
			return
		}
		nonce := base64.StdEncoding.EncodeToString(*nonceBytes)
		noncePool.Put(nonceBytes)

		// Сохраняем nonce в контексте для использования в шаблонах
		c.Set("csp_nonce", nonce)
		setCSPHeaders(c, nonce)

		c.Next()
	}
}

func setCSPHeaders(c *gin.Context, nonce string) {
	csp := "default-src 'self'; " +
		"script-src 'self' 'nonce-" + nonce + "' " + leafletJSHash + "; " +
		"style-src 'self' 'nonce-" + nonce + "' " + leafletCSSHash + "; " +
		"img-src 'self' data: https:; " +
		"connect-src 'self' ws: wss:; " +
		"frame-src 'self' https://www.youtube.com https://player.vimeo.com https://rutube.ru;"

	c.Header("Content-Security-Policy", csp)
	c.Header("X-Frame-Options", "DENY")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
	c.Header("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
	c.Header("Permissions-Policy", "geolocation=(), microphone=(), camera=(), payment=(), usb=(), fullscreen=(self), sync-xhr=(self), accelerometer=(), gyroscope=(), magnetometer=()")
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
