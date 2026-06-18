// internal/pkg/middleware/auth.go
package middleware

import (
    "net/http"
    "strings"

    "github.com/gin-gonic/gin"
)

// TokenParser – интерфейс для сервиса, умеющего проверять JWT и возвращать ID пользователя.
type TokenParser interface {
    ParseToken(tokenStr string) (uint, error)
}

// AuthRequired возвращает middleware, который проверяет JWT‑токен и сохраняет userID в контексте.
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

        userID, err := parser.ParseToken(token)
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
        c.Next()
    }
}

// OptionalAuth пытается извлечь userID из JWT-куки, но не прерывает запрос при её отсутствии.
// Если кука есть и токен валиден, userID сохраняется в контексте.
// Если куки нет или токен невалиден – просто передаём управление дальше без userID.
func OptionalAuth(parser TokenParser) gin.HandlerFunc {
    return func(c *gin.Context) {
        token, err := c.Cookie("jwt")
        if err == nil {
            if userID, err := parser.ParseToken(token); err == nil {
                c.Set("userID", userID)
            }
        }
        c.Next()
    }
}