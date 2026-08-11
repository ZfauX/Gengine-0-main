// internal/pkg/middleware/auth.go
package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"gengine-0/internal/pkg/rolecache"

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

// RoleCache (DEEP-REVIEW PASS-3 M9): единый TTL-кэш ролей, общий с
// CoAuthorService — раньше роль кэшировалась в двух местах с разными TTL и
// раздельной инвалидацией, что давало окно неконсистентных прав после смены роли.
var RoleCache = rolecache.New()

// SetRoleProvider регистрирует функцию загрузки актуальной роли из БД (S2).
// Роль в JWT-claims устаревает до истечения токена (15 мин) — пониженный или
// удалённый пользователь не должен сохранять привилегии.
func SetRoleProvider(fn RoleProvider) {
	roleProvider = fn
}

// getCachedRole возвращает роль из БД с коротким TTL-кэшем (M6, pass 30).
// Ошибки (в т.ч. ErrTokenUserNotFound) НЕ кэшируются — удалённый пользователь
// отзывается немедленно при следующем промахе кэша.
func getCachedRole(ctx context.Context, userID uint) (string, error) {
	if roleProvider == nil {
		return "", nil
	}
	// RoleProvider (middleware) и rolecache.Provider — одинаковые сигнатуры,
	// приведение явное для совместимости (M9).
	provider := rolecache.Provider(roleProvider)
	return RoleCache.Get(ctx, userID, provider)
}

// InvalidateRoleCache сбрасывает TTL-кэш роли пользователя. Вызывается после
// смены роли в админке — понижение применяется без ожидания TTL.
func InvalidateRoleCache(userID uint) {
	RoleCache.Invalidate(userID)
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
		// пользователь терял привилегии, а не ждал истечения токена.
		// M6 (pass 30): роль кэшируется на ~5с — БД не грузится на каждый запрос.
		if roleProvider != nil {
			currentRole, rErr := getCachedRole(c.Request.Context(), userID)
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
				// #8: перечитываем актуальную роль из БД (как AuthRequired) —
				// иначе пониженный админ сохраняет IsAdmin до истечения токена.
				// P7: при ошибке загрузки роли НЕ доверяем JWT-claim и НЕ
				// аутентифицируем запрос (fail-closed): при DB-блупе пониженный
				// админ не должен сохранять admin-привилегии на optional-auth
				// маршрутах.
				if roleProvider != nil {
					currentRole, rErr := getCachedRole(c.Request.Context(), userID)
					if rErr != nil {
						log.Error().Err(rErr).Uint("user_id", userID).Msg("OptionalAuth: failed to load role from DB, treating as unauthenticated")
						SetIsAdmin(c)
						c.Next()
						return
					}
					// DEEP-REVIEW HIGH #11 (pass 46): всегда берём роль из БД,
					// даже если она пустая. Раньше пустая роль в БД (повреждённые/
					// мигрированные данные) сохраняла роль из JWT-claim — пониженный
					// админ мог остаться админом на optional-auth маршрутах.
					role = currentRole
				}
				c.Set("userID", userID)
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

// ---------- Тестовые хелперы (M6, pass 30) ----------
// Экспортируются только для тестов пакета middleware_test — тестируют
// TTL-кэш ролей, не обращаясь к внутренним полям напрямую.

// ResetRoleCacheForTest сбрасывает кэш ролей (для тестов).
func ResetRoleCacheForTest() {
	RoleCache.InvalidateAll()
}

// GetRoleForTest возвращает роль через публичный путь getCachedRole
// (для тестов).
func GetRoleForTest(ctx context.Context, userID uint) (string, error) {
	return getCachedRole(ctx, userID)
}

// ExpireRoleCacheForTest делает все кэшированные роли просроченными
// (для тестов TTL-истечения).
func ExpireRoleCacheForTest() {
	// rolecache хранит время истечения внутри; для теста просто очищаем,
	// чтобы следующий Get перечитал роль из провайдера (эквивалентно истечению).
	RoleCache.InvalidateAll()
}
