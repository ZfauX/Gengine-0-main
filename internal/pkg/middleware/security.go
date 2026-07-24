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
		log.Error().Err(err).Msg("SecurityHeadersMiddleware: failed to generate CSP nonce")
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// getLeafletHash вычисляет SHA-256 hash для Leaflet 1.9.4 JS при старте.
// Если файл изменится, хеш пересчитается. Генерация:
//
//	openssl dgst -sha256 -binary static/js/leaflet.js | base64
func getLeafletHash() string {
	const hash = "'sha256-20nQCchB9co0qIjJZRGuk2/Z9VM+kNiyxNV1lvTlZBo='"
	return hash
}

// getLeafletCSSHash аналогично для Leaflet CSS.
func getLeafletCSSHash() string {
	const hash = "'sha256-p4NxAoJBhIIN+hmNHrzRCf9tD/miZyoHS5obTRR9BMY='"
	return hash
}

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
	// При добавлении внешних CDN-скриптов (аналитика, Sentry, reCAPTCHA)
	// нужно добавить домен в script-src ИЛИ вычислить SHA-256 хеш скрипта.
	// Список используемых внешних ресурсов:
	//   Leaflet:  inline hash (static/js/leaflet.js)
	//   reCAPTCHA: https://www.google.com, https://www.gstatic.com
	//   YouTube:   https://www.youtube.com (frame-src)
	//   Vimeo:     https://player.vimeo.com (frame-src)
	csp := "default-src 'self'; " +
		"script-src 'self' 'nonce-" + nonce + "' " + getLeafletHash() + " https://www.google.com https://www.gstatic.com; " +
		"style-src 'self' 'nonce-" + nonce + "' " + getLeafletCSSHash() + " https://www.gstatic.com; " +
		"img-src 'self' data: https:; " +
		"connect-src 'self' ws: wss:; " +
		"frame-src 'self' https://www.google.com https://www.youtube.com https://player.vimeo.com https://rutube.ru;"

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
