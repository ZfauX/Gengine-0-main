// internal/domain/user/handler.go
package user

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const avatarMaxSize = 2 * 1024 * 1024

func isHTTPS(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}
	if proto := c.GetHeader("X-Forwarded-Proto"); proto == "https" {
		return true
	}
	if proto := c.GetHeader("X-Forwarded-Protocol"); strings.HasSuffix(proto, "s") {
		return true
	}
	if proto := c.GetHeader("X-Url-Scheme"); proto == "https" {
		return true
	}
	return false
}

func setSecureCookie(c *gin.Context, name, value string, maxAge int, path string) {
	c.SetSameSite(http.SameSiteStrictMode)
	secure := isHTTPS(c)
	c.SetCookie(name, value, maxAge, path, "", secure, true)
}

type UserIDRequest struct {
	ID uint `uri:"id" json:"id" binding:"required,gt=0"`
}

type OAuthProviderRequest struct {
	Provider string `uri:"provider" json:"provider" binding:"required,oneof=google github yandex"`
}

type VerifyEmailRequest struct {
	Code string `form:"code" json:"code" binding:"required,len=8"`
}

type RegisterInput struct {
	Email    string `form:"email" json:"email" binding:"required,email"`
	Password string `form:"password" json:"password" binding:"required,min=6,max=72"`
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
