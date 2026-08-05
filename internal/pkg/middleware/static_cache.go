package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
)

func StaticCacheMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/static/") {
			// Версионированные ассеты (?v=...) — контент-хэш в URL: immutable.
			// Неверсионированные (leaflet.js/css, иконки) — короткий max-age,
			// иначе после обновления они остаются в кэше браузера навсегда (P4).
			if strings.Contains(c.Request.URL.RawQuery, "v=") {
				c.Header("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				c.Header("Cache-Control", "public, max-age=3600")
			}
		} else if strings.HasPrefix(path, "/uploads/") {
			c.Header("Cache-Control", "no-cache, must-revalidate")
		}
		c.Next()
	}
}
