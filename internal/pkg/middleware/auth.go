// internal/pkg/middleware/auth.go
package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// TokenParser – интерфейс для сервиса, умеющего проверять JWT и возвращать ID пользователя и его роль.
type TokenParser interface {
	ParseToken(tokenStr string) (uint, string, error) // возвращает userID, role, error
}

// RoleProvider возвращает актуальную роль пользователя из БД.
// Настраивается один раз при старте приложения через SetRoleProvider.
type RoleProvider func(ctx context.Context, userID uint) (string, error)

// ErrTokenUserNotFound — пользователь токена удалён/не существует: токен недействителен.
var ErrTokenUserNotFound = errors.New("пользователь не найден")

var roleProvider RoleProvider

// SetRoleProvider регистрирует функцию загрузки актуальной роли из БД (S2).
// Роль в JWT-claims устаревает до истечения токена (15 мин) — пониженный или
// удалённый пользователь не должен сохранять привилегии.
func SetRoleProvider(fn RoleProvider) {
	roleProvider = fn
}

// AuthRequired возвращает middleware, который проверяет JWT‑токен и сохраняет userID и role в контексте.
// Если токена нет или он невалиден – перенаправляет на /auth/login.
func AuthRequired(parser TokenParser) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie("jwt")
		if err != nil {
			if strings.HasPrefix(c.Request.URL.Path, "/api/") {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": ErrAuthRequired.Error()})
				return
			}
			c.Redirect(http.StatusFound, "/auth/login")
			c.Abort()
			return
		}

		userID, role, err := parser.ParseToken(token)
		if err != nil {
			if strings.HasPrefix(c.Request.URL.Path, "/api/") {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": ErrInvalidToken.Error()})
				return
			}
			c.Redirect(http.StatusFound, "/auth/login")
			c.Abort()
			return
		}

		c.Set("userID", userID)
		// Перечитываем актуальную роль из БД (S2), чтобы пониженный/удалённый
		// пользователь терял привилегии немедленно, а не по истечении токена.
		if roleProvider != nil {
			currentRole, rErr := roleProvider(c.Request.Context(), userID)
			switch {
			case errors.Is(rErr, ErrTokenUserNotFound):
				if strings.HasPrefix(c.Request.URL.Path, "/api/") {
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": ErrInvalidToken.Error()})
				} else {
					c.Redirect(http.StatusFound, "/auth/login")
				}
				c.Abort()
				return
			case rErr != nil:
				log.Error().Err(rErr).Uint("user_id", userID).Msg("AuthRequired: failed to load role from DB")
				c.AbortWithStatus(http.StatusInternalServerError)
				return
			default:
				role = currentRole
			}
		}
		c.Set("role", role) // сохраняем роль
		SetIsAdmin(c)
		loadThemeSettings(c)
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
				// #8: перечитываем актуальную роль из БД (как AuthRequired) —
				// иначе пониженный админ сохраняет IsAdmin до истечения токена.
				if roleProvider != nil {
					currentRole, rErr := roleProvider(c.Request.Context(), userID)
					if rErr == nil && currentRole != "" {
						role = currentRole
					}
				}
				c.Set("role", role)
				loadThemeSettings(c)
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
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": ErrAuthRequired.Error()})
			return
		}

		roleStr, ok := role.(string)
		if !ok || roleStr != "admin" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": ErrAccessDenied.Error()})
			return
		}

		c.Set("IsAdmin", true)
		c.Next()
	}
}
