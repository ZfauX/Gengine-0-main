package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
)

func StaticCacheMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/static/") {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		} else if strings.HasPrefix(path, "/uploads/") {
			c.Header("Cache-Control", "no-cache, must-revalidate")
		}
		c.Next()
	}
}