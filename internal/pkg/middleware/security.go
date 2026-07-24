// internal/pkg/middleware/security.go
package middleware

import (
	"crypto/rand"
	"encoding/base64"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// generateNonce создаёт криптостойкий случайный nonce для CSP.
// Использует crypto/rand — достаточно быстрый для per-request генерации.
// При отказе crypto/rand паникуем, так как небезопасный nonce хуже его отсутствия.
func generateNonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		log.Panic().Err(err).Msg("crypto/rand failed, cannot generate secure nonce")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// getLeafletHash возвращает SHA-256 hash для Leaflet 1.9.4 JS.
// Вычисляется: openssl dgst -sha256 -binary leaflet.js | base64
const leafletJSHash = "'sha256-20nQCchB9co0qIjJZRGuk2/Z9VM+kNiyxNV1lvTlZBo='"

// getLeafletCSSHash возвращает SHA-256 hash для Leaflet 1.9.4 CSS.
const leafletCSSHash = "'sha256-p4NxAoJBhIIN+hmNHrzRCf9tD/miZyoHS5obTRR9BMY='"

// SecurityHeadersMiddleware добавляет базовые защитные заголовки ко всем ответам.
func SecurityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		nonce := generateNonce()

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

	// HSTS только если запрос пришёл по HTTPS (проверяем X-Forwarded-Proto или scheme)
	if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
		c.Header("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
	}

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
