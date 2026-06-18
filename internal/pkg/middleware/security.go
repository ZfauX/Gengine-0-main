// internal/pkg/middleware/security.go
package middleware

import (
	"github.com/gin-gonic/gin"
)

// SecurityHeadersMiddleware добавляет базовые защитные заголовки ко всем ответам.
func SecurityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Заголовок Content-Security-Policy – разрешаем ресурсы только с собственного домена и CDN
		c.Header("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net; "+
				"style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net https://unpkg.com; "+
				"img-src 'self' data: https:; "+
				"connect-src 'self' ws: wss:; "+
				"frame-src 'self' https://www.youtube.com https://player.vimeo.com https://rutube.ru;")

		// Запрещаем открытие сайта во фрейме (защита от clickjacking)
		c.Header("X-Frame-Options", "DENY")

		// Запрещаем MIME-type sniffing
		c.Header("X-Content-Type-Options", "nosniff")

		// Управляем передачей Referer
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")

		// Принудительное использование HTTPS в production (можно активировать по условию)
		// c.Header("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")

		c.Next()
	}
}