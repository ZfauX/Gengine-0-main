// internal/pkg/middleware/auth_test.go
package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"gengine-0/internal/pkg/middleware"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubTokenParser struct {
	parseResult uint
	parseError  error
}

func (s *stubTokenParser) ParseToken(token string) (uint, error) {
	return s.parseResult, s.parseError
}

func TestAuthRequired_NoCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.AuthRequired(&stubTokenParser{}))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Without cookie, expecting redirect to login
	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/auth/login", w.Header().Get("Location"))
}

func TestAuthRequired_InvalidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	parser := &stubTokenParser{parseError: assert.AnError}
	router := gin.New()
	router.Use(middleware.AuthRequired(parser))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.AddCookie(&http.Cookie{Name: "jwt", Value: "invalid"})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/auth/login", w.Header().Get("Location"))
}

func TestAuthRequired_ValidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	parser := &stubTokenParser{parseResult: 42}
	router := gin.New()
	router.Use(middleware.AuthRequired(parser))
	router.GET("/test", func(c *gin.Context) {
		userID, exists := c.Get("userID")
		require.True(t, exists)
		assert.Equal(t, uint(42), userID)
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.AddCookie(&http.Cookie{Name: "jwt", Value: "valid"})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAuthRequired_API_NoCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.AuthRequired(&stubTokenParser{}))
	router.GET("/api/v1/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{})
	})

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "требуется аутентификация")
}

func TestAuthRequired_API_InvalidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	parser := &stubTokenParser{parseError: assert.AnError}
	router := gin.New()
	router.Use(middleware.AuthRequired(parser))
	router.GET("/api/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{})
	})

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.AddCookie(&http.Cookie{Name: "jwt", Value: "bad"})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "невалидный токен")
}