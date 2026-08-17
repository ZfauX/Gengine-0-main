// internal/pkg/middleware/security.go
package middleware

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/gin-gonic/gin"
)

// generateNonce создаёт криптостойкий случайный nonce для CSP.
// Использует crypto/rand — достаточно быстрый для per-request генерации.
// При отказе crypto/rand паникуем, так как небезопасный nonce хуже его отсутствия.
func generateNonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("SecurityHeadersMiddleware: crypto/rand generation of CSP nonce failed: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// leafletHashes — вычисленные при старте SRI-хэши Leaflet (L11, PASS-22).
// Раньше были захардкожены — при обновлении файла CSP блокировал бы скрипт.
// Теперь считаются из фактических файлов; при отсутствии файла — fallback.
var (
	leafletHashOnce sync.Once
	leafletHash     = "'sha256-20nQCchB9co0qIjJZRGuk2/Z9VM+kNiyxNV1lvTlZBo='"
	leafletCSSHash  = "'sha256-p4NxAoJBhIIN+hmNHrzRCf9tD/miZyoHS5obTRR9BMY='"
)

// initLeafletHashes вычисляет SHA-256 хэши static/js/leaflet.js и
// static/css/leaflet.css. Генерация: openssl dgst -sha256 -binary <file> | base64
func initLeafletHashes(staticDir string) {
	leafletHashOnce.Do(func() {
		if h := sha256File(filepath.Join(staticDir, "js", "leaflet.js")); h != "" {
			leafletHash = "'sha256-" + h + "'"
		}
		if h := sha256File(filepath.Join(staticDir, "css", "leaflet.css")); h != "" {
			leafletCSSHash = "'sha256-" + h + "'"
		}
	})
}

// sha256File возвращает base64(SHA-256(file)) или "" при ошибке чтения.
func sha256File(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func getLeafletHash() string {
	return leafletHash
}

func getLeafletCSSHash() string {
	return leafletCSSHash
}

// SecurityHeadersMiddleware добавляет базовые защитные заголовки ко всем ответам.
// forceHSTS — сервер обслуживается по HTTPS (собственный TLS или reverse-proxy,
// который терминирует TLS). В этом случае HSTS отправляем всегда, а не только
// когда прокси прокинул X-Forwarded-Proto (иначе HSTS может никогда не прийти).
// staticDir (L11, PASS-22): путь к статическим файлам для вычисления SRI-хэшей
// Leaflet при старте.
func SecurityHeadersMiddleware(forceHSTS bool, staticDir string) gin.HandlerFunc {
	initLeafletHashes(staticDir)
	return func(c *gin.Context) {
		nonce := generateNonce()

		c.Set("csp_nonce", nonce)
		setCSPHeaders(c, nonce, forceHSTS)

		c.Next()
	}
}

func setCSPHeaders(c *gin.Context, nonce string, forceHSTS bool) {
	// При добавлении внешних CDN-скриптов (аналитика, Sentry, reCAPTCHA)
	// нужно добавить домен в script-src ИЛИ вычислить SHA-256 хеш скрипта.
	// Список используемых внешних ресурсов:
	//   Leaflet:  inline hash (static/js/leaflet.js)
	//   reCAPTCHA: https://www.google.com, https://www.gstatic.com
	//   YouTube:   https://www.youtube.com (frame-src)
	//   Vimeo:     https://player.vimeo.com (frame-src)
	csp := "default-src 'self'; " +
		"script-src 'self' 'nonce-" + nonce + "' " + getLeafletHash() + " https://www.google.com https://www.gstatic.com; " +
		// S-4 (pass 36): убран 'unsafe-inline' — единственный <style> блок уже
		// с nonce, inline style-атрибутов в шаблонах нет. Tailwind компилируется
		// в output.css (style-src 'self').
		"style-src 'self' 'nonce-" + nonce + "' " + getLeafletCSSHash() + " https://www.gstatic.com; " +
		"img-src 'self' data: https:; " +
		"connect-src 'self' ws: wss:; " +
		"form-action 'self'; " +
		"frame-src 'self' https://www.google.com https://www.youtube.com https://player.vimeo.com https://rutube.ru; " +
		// L10 (PASS-3): закрываем плагины, base-URI и фрейминг нашего сайта.
		"object-src 'none'; " +
		"base-uri 'none'; " +
		"frame-ancestors 'none';"

	c.Header("Content-Security-Policy", csp)
	c.Header("X-Frame-Options", "DENY")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "strict-origin-when-cross-origin")

	// HSTS: отправляем, если запрос пришёл по HTTPS, через reverse proxy (X-Forwarded-Proto),
	// либо если сервер гарантированно обслуживается по HTTPS (forceHSTS).
	if forceHSTS || c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
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
