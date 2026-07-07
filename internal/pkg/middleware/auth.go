// internal/pkg/middleware/auth.go
package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// TokenParser – интерфейс для сервиса, умеющего проверять JWT и возвращать ID пользователя и его роль.
type TokenParser interface {
	ParseToken(tokenStr string) (uint, string, error) // возвращает userID, role, error
}

// AuthRequired возвращает middleware, который проверяет JWT‑токен и сохраняет userID и role в контексте.
// Если токена нет или он невалиден – перенаправляет на /auth/login.
func AuthRequired(parser TokenParser) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie("jwt")
		if err != nil {
			if strings.HasPrefix(c.Request.URL.Path, "/api/") {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "требуется аутентификация"})
				return
			}
			c.Redirect(http.StatusFound, "/auth/login")
			c.Abort()
			return
		}

		userID, role, err := parser.ParseToken(token)
		if err != nil {
			if strings.HasPrefix(c.Request.URL.Path, "/api/") {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "невалидный токен"})
				return
			}
			c.Redirect(http.StatusFound, "/auth/login")
			c.Abort()
			return
		}

		c.Set("userID", userID)
		c.Set("role", role) // сохраняем роль
		SetIsAdmin(c)
		c.Next()
	}
}

// OptionalAuth пытается извлечь userID и role из JWT-куки, но не прерывает запрос при её отсутствии.
// Если кука есть и токен валиден, userID и role сохраняются в контексте.
// Если куки нет или токен невалиден – просто передаём управление дальше без userID/role.
// После этого автоматически устанавливает IsAdmin в контекст.
func OptionalAuth(parser TokenParser) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie("jwt")
		if err == nil {
			if userID, role, err := parser.ParseToken(token); err == nil {
				c.Set("userID", userID)
				c.Set("role", role)
			}
		}
		SetIsAdmin(c)
		c.Next()
	}
}

// AdminRequired возвращает middleware, который проверяет, что текущий пользователь является администратором.
// Требует, чтобы перед ним был использован AuthRequired (т.е. userID и role уже установлены в контексте).
// Теперь не требует передачи *gorm.DB, так как роль извлекается из JWT.
func AdminRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, exists := c.Get("role")
		if !exists {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "требуется аутентификация"})
			return
		}

		roleStr, ok := role.(string)
		if !ok || roleStr != "admin" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "доступ запрещён"})
			return
		}

		c.Set("IsAdmin", true)
		c.Next()
	}
}
