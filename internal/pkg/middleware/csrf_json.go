// internal/pkg/middleware/csrf_json.go
package middleware

import (
	"net/http"
	"strings"

	csrf "gengine-0/internal/pkg/csrf"

	"github.com/gin-gonic/gin"
)

// CSRFJSON возвращает middleware, которое проверяет CSRF-токен для всех мутирующих запросов.
// Для GET/HEAD/OPTIONS запросов проверка не выполняется.
// Токен ожидается в заголовке "X-CSRF-Token" (для JSON) или в теле формы (для HTML-форм).
func CSRFJSON() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Для безопасных методов CSRF не требуется
		if c.Request.Method == "GET" || c.Request.Method == "HEAD" || c.Request.Method == "OPTIONS" {
			c.Next()
			return
		}

		var token string
		contentType := c.GetHeader("Content-Type")
		if strings.Contains(contentType, "application/json") {
			token = c.GetHeader("X-CSRF-Token")
		} else {
			// Для form-urlencoded/multipart — токен из тела формы
			token = c.PostForm("_csrf")
		}

		if token == "" {
			c.JSON(http.StatusForbidden, gin.H{
				"error": "CSRF token missing",
				"code":  "csrf_missing",
			})
			c.Abort()
			return
		}

		validToken := csrf.GetToken(c)
		if token != validToken {
			c.JSON(http.StatusForbidden, gin.H{
				"error": "CSRF token mismatch",
				"code":  "csrf_mismatch",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
