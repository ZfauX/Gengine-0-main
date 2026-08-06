// internal/domain/user/webauthn_handler_test.go
package user

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

func newWebAuthnTestRouter(h *WebAuthnHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	store := cookie.NewStore([]byte("test-session-secret-32chars-long!!!"))
	router.Use(sessions.Sessions("gengine_test_session", store))
	router.Use(func(c *gin.Context) {
		c.Set("userID", uint(1))
		c.Next()
	})
	router.POST("/auth/webauthn/register/begin", h.BeginRegistration)
	return router
}

// TestWebAuthnBeginRegistration_Requires2FA: регистрация passkey у пользователя
// с включённой 2FA отклоняется (403) без подтверждённой 2FA-сессии (T-2/S-H1).
func TestWebAuthnBeginRegistration_Requires2FA(t *testing.T) {
	mockRepo := &mockUserRepo{users: map[uint]*User{
		1: {Model: gorm.Model{ID: 1}, TwoFactorEnabled: true},
	}}
	h := &WebAuthnHandler{userRepo: mockRepo}
	router := newWebAuthnTestRouter(h)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/auth/webauthn/register/begin", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code, "регистрация passkey без 2FA должна быть отклонена")
}
