// internal/pkg/middleware/csrf_json.go
package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	csrf "github.com/utrack/gin-csrf"
)

// CSRFJSON возвращает middleware, которое проверяет CSRF-токен для JSON-запросов.
// Для GET/HEAD/OPTIONS запросов проверка не выполняется.
// Токен ожидается в заголовке "X-CSRF-Token".
func CSRFJSON() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Для безопасных методов CSRF не требуется
		if c.Request.Method == "GET" || c.Request.Method == "HEAD" || c.Request.Method == "OPTIONS" {
			c.Next()
			return
		}

		// Проверяем Content-Type - если это JSON, используем заголовок
		contentType := c.GetHeader("Content-Type")
		if strings.Contains(contentType, "application/json") {
			token := c.GetHeader("X-CSRF-Token")
			if token == "" {
				c.JSON(http.StatusForbidden, gin.H{
					"error": "CSRF token missing",
					"code":  "csrf_missing",
				})
				c.Abort()
				return
			}

			// Получаем токен из middleware
			validToken := csrf.GetToken(c)
			if token != validToken {
				c.JSON(http.StatusForbidden, gin.H{
					"error": "CSRF token mismatch",
					"code":  "csrf_mismatch",
				})
				c.Abort()
				return
			}
		}

		c.Next()
	}
}
