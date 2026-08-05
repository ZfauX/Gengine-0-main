// internal/domain/user/handler.go
package user

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"gengine-0/internal/config"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

const avatarMaxSize = 2 * 1024 * 1024

// SetSecureCookieConfig is injected by the caller (set in routes.go)
// to control Secure flag behavior behind reverse proxies.
var SetSecureCookieConfig *config.ServerConfig

// clientFingerprint вычисляет SHA256(User-Agent + "|" + IP-префикс) для привязки refresh-токена к клиенту.
// Это prevents использование украденного refresh-токена с другого устройства/браузера.
func clientFingerprint(c *gin.Context) string {
	userAgent := c.Request.UserAgent()
	ip := c.ClientIP()
	// Use a stable IP prefix: IPv4 /24, IPv6 /64
	ipPrefix := normalizeIPPrefix(ip)
	data := userAgent + "|" + ipPrefix
	h := sha256.Sum256([]byte(data))
	return hex.EncodeToString(h[:])
}

// normalizeIPPrefix возвращает стабильный префикс IP: IPv4 — первые 3 октета, IPv6 — первые 4 группы.
func normalizeIPPrefix(ip string) string {
	if strings.Contains(ip, ":") {
		// IPv6 — use /64 prefix (first 4 groups)
		parts := strings.SplitN(ip, ":", 5)
		if len(parts) >= 4 {
			return strings.Join(parts[:4], ":")
		}
		return ip
	}
	parts := strings.SplitN(ip, ".", 4)
	if len(parts) >= 3 {
		return strings.Join(parts[:3], ".")
	}
	return ip
}

func isHTTPS(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}
	// Check X-Forwarded-Proto for reverse proxy setups
	if fwdProto := c.GetHeader("X-Forwarded-Proto"); fwdProto == "https" {
		return true
	}
	// Force Secure if configured (e.g., when behind TLS-terminating proxy)
	if SetSecureCookieConfig != nil && SetSecureCookieConfig.ForceSecureCookie {
		return true
	}
	return false
}

func setSecureCookie(c *gin.Context, name, value string, maxAge int, path string) {
	c.SetSameSite(http.SameSiteStrictMode)
	secure := isHTTPS(c)
	if !secure {
		log.Warn().Str("cookie", name).Msg("setSecureCookie: Secure flag not set (HTTPS not detected)")
	}
	c.SetCookie(name, value, maxAge, path, "", secure, true)
}

// SearchUsersAPI ищет пользователей по имени или email.
// Email возвращается только для администраторов; для остальных — маскируется.
func SearchUsersAPI(userRepo UserRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		q := c.DefaultQuery("q", "")
		if len(q) < 2 {
			c.JSON(http.StatusOK, gin.H{"users": []gin.H{}})
			return
		}
		isAdmin := middleware.IsAdmin(c)

		users, err := userRepo.SearchUsersLight(c.Request.Context(), q, 10)
		if err != nil {
			log.Error().Err(err).Msg("SearchUsersAPI: query failed")
			c.JSON(http.StatusInternalServerError, gin.H{"error": render.Tr(c, "handler.operation_failed")})
			return
		}
		results := make([]gin.H, 0, len(users))
		for _, u := range users {
			email := u.Email
			if !isAdmin {
				email = maskEmail(email)
			}
			results = append(results, gin.H{"id": u.ID, "name": u.Name, "email": email})
		}
		c.JSON(http.StatusOK, gin.H{"users": results})
	}
}

// maskEmail маскирует email для защиты PII: j***@example.com
func maskEmail(email string) string {
	if email == "" {
		return ""
	}
	at := strings.Index(email, "@")
	if at <= 1 {
		// Однобуквенный локальный логин — маскируем полностью локальную часть
		return "***" + email[at:]
	}
	return email[:1] + "***" + email[at:]
}

// safeReturnURL валидирует return_url: только same-origin пути (начинаются с "/", не с "//").
func safeReturnURL(raw string, fallback string) string {
	if raw == "" {
		return fallback
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return fallback
	}
	// Отклоняем URL с контрольными символами
	if strings.ContainsAny(raw, "\r\n") {
		return fallback
	}
	return raw
}

type UserIDRequest struct {
	ID uint `uri:"id" json:"id" binding:"required,gt=0"`
}

type OAuthProviderRequest struct {
	Provider string `uri:"provider" json:"provider" binding:"required,oneof=vk yandex"`
}

type VerifyEmailRequest struct {
	Code string `form:"code" json:"code" binding:"required,len=12"`
}

type RegisterInput struct {
	Email    string `form:"email" json:"email" binding:"required,email"`
	Password string `form:"password" json:"password" binding:"required,min=8,max=72"`
	Name     string `form:"name" json:"name" binding:"required,min=2,max=50"`
}

type LoginInput struct {
	Email    string `form:"email" json:"email" binding:"required,email"`
	Password string `form:"password" json:"password" binding:"required"`
}

type ForgotInput struct {
	Email string `form:"email" json:"email" binding:"required,email"`
}

type ResetInput struct {
	ResetCode string `form:"reset_code" binding:"required"`
	Password  string `form:"password" json:"password" binding:"required,min=8,max=72"`
}

type UpdateProfileInput struct {
	Name  string `form:"name" json:"name" binding:"required,min=2,max=50"`
	Email string `form:"email" json:"email" binding:"required,email"`
}

type ChangePasswordInput struct {
	OldPassword string `form:"old_password" json:"old_password" binding:"required"`
	NewPassword string `form:"new_password" json:"new_password" binding:"required,min=8,max=72"`
}

type RefreshTokenInput struct {
	RefreshToken string `form:"refresh_token" json:"refresh_token" binding:"required"`
}
