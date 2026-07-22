// internal/pkg/middleware/middleware_test.go
package middleware_test

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gengine-0/internal/pkg/middleware"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Моки для тестов
// =============================================================================

// +++ Изменяем stubTokenParser: ParseToken теперь возвращает (uint, string, error)
type stubTokenParser struct {
	parseResult uint
	parseError  error
	role        string // опционально, для тестов
}

func (s *stubTokenParser) ParseToken(token string) (uint, string, error) {
	return s.parseResult, s.role, s.parseError
}

type stubGameAuthorizer struct {
	isManager bool
	err       error
}

func (s *stubGameAuthorizer) IsUserManager(ctx context.Context, gameID, userID uint) (bool, error) {
	return s.isManager, s.err
}

type stubTeamAccessChecker struct {
	canManage bool
}

func (s *stubTeamAccessChecker) CanManageTeam(teamID, userID uint) bool {
	return s.canManage
}

// =============================================================================
// Вспомогательная функция для тестов AdminRequired (больше не нужна, удаляем)
// =============================================================================

// setupAdminTestDB больше не нужна, так как AdminRequired не обращается к БД.
// Удаляем эту функцию и все тесты, которые её используют, или переписываем их.

// =============================================================================
// Тесты для AuthRequired (дополняем уже существующие)
// =============================================================================

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

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/auth/login", w.Header().Get("Location"))
}

func TestAuthRequired_InvalidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	parser := &stubTokenParser{parseError: errors.New("invalid")}
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
		// Проверяем, что роль тоже установлена (пустая)
		role, _ := c.Get("role")
		assert.Equal(t, "", role)
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
	assert.Contains(t, w.Body.String(), middleware.ErrAuthRequired)
}

func TestAuthRequired_API_InvalidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	parser := &stubTokenParser{parseError: errors.New("invalid")}
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
	assert.Contains(t, w.Body.String(), middleware.ErrInvalidToken)
}

// =============================================================================
// Тесты для OptionalAuth
// =============================================================================

func TestOptionalAuth_NoCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	parser := &stubTokenParser{}
	router := gin.New()
	router.Use(middleware.OptionalAuth(parser))
	router.GET("/test", func(c *gin.Context) {
		_, exists := c.Get("userID")
		assert.False(t, exists)
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestOptionalAuth_ValidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	parser := &stubTokenParser{parseResult: 99}
	router := gin.New()
	router.Use(middleware.OptionalAuth(parser))
	router.GET("/test", func(c *gin.Context) {
		userID, exists := c.Get("userID")
		require.True(t, exists)
		assert.Equal(t, uint(99), userID)
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.AddCookie(&http.Cookie{Name: "jwt", Value: "valid"})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestOptionalAuth_InvalidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	parser := &stubTokenParser{parseError: errors.New("invalid")}
	router := gin.New()
	router.Use(middleware.OptionalAuth(parser))
	router.GET("/test", func(c *gin.Context) {
		_, exists := c.Get("userID")
		assert.False(t, exists)
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.AddCookie(&http.Cookie{Name: "jwt", Value: "bad"})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// =============================================================================
// Тесты для AdminRequired (больше не используем БД, только контекст)
// =============================================================================

func TestAdminRequired_UserNotAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	// Устанавливаем userID и роль "user" в контексте
	router.Use(func(c *gin.Context) {
		c.Set("userID", uint(1))
		c.Set("role", "user")
		c.Next()
	})
	router.Use(middleware.AdminRequired())
	router.GET("/admin", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("GET", "/admin", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), middleware.ErrAccessDenied)
}

func TestAdminRequired_AdminUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", uint(1))
		c.Set("role", "admin")
		c.Next()
	})
	router.Use(middleware.AdminRequired())
	router.GET("/admin", func(c *gin.Context) {
		isAdmin, exists := c.Get("IsAdmin")
		require.True(t, exists)
		assert.True(t, isAdmin.(bool))
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("GET", "/admin", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAdminRequired_NoUserID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.AdminRequired())
	router.GET("/admin", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("GET", "/admin", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), middleware.ErrAuthRequired)
}

// =============================================================================
// Тесты для GameManager
// =============================================================================

func TestGameManager_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authorizer := &stubGameAuthorizer{isManager: true}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", uint(5))
		c.Next()
	})
	router.Use(middleware.GameManager(authorizer))
	router.GET("/game/:id", func(c *gin.Context) {
		isManager, exists := c.Get("isGameManager")
		require.True(t, exists)
		assert.True(t, isManager.(bool))
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("GET", "/game/123", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGameManager_NotManager(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authorizer := &stubGameAuthorizer{isManager: false}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", uint(5))
		c.Next()
	})
	router.Use(middleware.GameManager(authorizer))
	router.GET("/game/:id", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("GET", "/game/123", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestGameManager_NoUserID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authorizer := &stubGameAuthorizer{}
	router := gin.New()
	router.Use(middleware.GameManager(authorizer))
	router.GET("/game/:id", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("GET", "/game/123", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGameManager_InvalidGameID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authorizer := &stubGameAuthorizer{}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", uint(1))
		c.Next()
	})
	router.Use(middleware.GameManager(authorizer))
	router.GET("/game/:id", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("GET", "/game/notanumber", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// =============================================================================
// Тесты для RequirePermission
// =============================================================================

func TestRequirePermission_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authorizer := &stubGameAuthorizer{isManager: true}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", uint(1))
		c.Next()
	})
	router.Use(middleware.RequirePermission(authorizer, "editor"))
	router.GET("/game/:game_id", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("GET", "/game/123", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequirePermission_NotAuthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authorizer := &stubGameAuthorizer{isManager: false}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", uint(1))
		c.Next()
	})
	router.Use(middleware.RequirePermission(authorizer, "editor"))
	router.GET("/game/:game_id", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("GET", "/game/123", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), middleware.ErrInsufficientRights)
}

func TestRequirePermission_NoUserID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authorizer := &stubGameAuthorizer{}
	router := gin.New()
	router.Use(middleware.RequirePermission(authorizer, "editor"))
	router.GET("/game/:game_id", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("GET", "/game/123", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequirePermission_InvalidGameID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authorizer := &stubGameAuthorizer{}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", uint(1))
		c.Next()
	})
	router.Use(middleware.RequirePermission(authorizer, "editor"))
	router.GET("/game/:game_id", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("GET", "/game/abc", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), middleware.ErrInvalidGameID)
}

// =============================================================================
// Тесты для TeamCaptainOrGameAuthor
// =============================================================================

func TestTeamCaptainOrGameAuthor_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	checker := &stubTeamAccessChecker{canManage: true}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", uint(1))
		c.Next()
	})
	router.Use(middleware.TeamCaptainOrGameAuthor(checker))
	router.GET("/team/:team_id", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("GET", "/team/456", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestTeamCaptainOrGameAuthor_Forbidden(t *testing.T) {
	gin.SetMode(gin.TestMode)
	checker := &stubTeamAccessChecker{canManage: false}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", uint(1))
		c.Next()
	})
	router.Use(middleware.TeamCaptainOrGameAuthor(checker))
	router.GET("/team/:team_id", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("GET", "/team/456", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestTeamCaptainOrGameAuthor_NoUserID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	checker := &stubTeamAccessChecker{}
	router := gin.New()
	router.Use(middleware.TeamCaptainOrGameAuthor(checker))
	router.GET("/team/:team_id", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("GET", "/team/456", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestTeamCaptainOrGameAuthor_InvalidTeamID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	checker := &stubTeamAccessChecker{}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", uint(1))
		c.Next()
	})
	router.Use(middleware.TeamCaptainOrGameAuthor(checker))
	router.GET("/team/:team_id", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("GET", "/team/notnumber", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// =============================================================================
// Тесты для ContextTimeout
// =============================================================================

func TestContextTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ContextTimeout(50 * time.Millisecond))
	router.GET("/test", func(c *gin.Context) {
		select {
		case <-time.After(100 * time.Millisecond):
			c.String(http.StatusOK, "done")
		case <-c.Request.Context().Done():
			c.String(http.StatusGatewayTimeout, "timeout")
		}
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusGatewayTimeout, w.Code)
	assert.Equal(t, "timeout", w.Body.String())
}

func TestContextTimeout_WithinLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ContextTimeout(100 * time.Millisecond))
	router.GET("/test", func(c *gin.Context) {
		select {
		case <-time.After(10 * time.Millisecond):
			c.String(http.StatusOK, "done")
		case <-c.Request.Context().Done():
			c.String(http.StatusGatewayTimeout, "timeout")
		}
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "done", w.Body.String())
}

// =============================================================================
// Тесты для GzipMiddleware
// =============================================================================

func TestGzipMiddleware_WithAcceptEncoding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.GzipMiddleware())
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "hello world")
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "gzip", w.Header().Get("Content-Encoding"))
	assert.Equal(t, "Accept-Encoding", w.Header().Get("Vary"))

	reader, err := gzip.NewReader(w.Body)
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()
	decompressed, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(decompressed))
}

func TestGzipMiddleware_NoAcceptEncoding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.GzipMiddleware())
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "hello world")
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("Content-Encoding"))
	assert.Equal(t, "hello world", w.Body.String())
}

func TestGzipMiddleware_SkipsMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.GzipMiddleware())
	router.GET("/metrics", func(c *gin.Context) {
		c.String(http.StatusOK, "metrics data")
	})

	req := httptest.NewRequest("GET", "/metrics", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("Content-Encoding"))
	assert.Equal(t, "metrics data", w.Body.String())
}

// =============================================================================
// Тесты для RateLimiter (структура и middleware)
// =============================================================================

func TestRateLimiter_AllowWithinLimit(t *testing.T) {
	rl := middleware.NewRateLimiter(1*time.Second, 3)
	defer rl.Stop()

	for i := 0; i < 3; i++ {
		assert.True(t, rl.Allow("key1").Allowed)
	}
	assert.False(t, rl.Allow("key1").Allowed)
}

func TestRateLimiter_AllowAfterWindow(t *testing.T) {
	rl := middleware.NewRateLimiter(100*time.Millisecond, 2)
	defer rl.Stop()

	assert.True(t, rl.Allow("key").Allowed)
	assert.True(t, rl.Allow("key").Allowed)
	assert.False(t, rl.Allow("key").Allowed)

	// Ждём истечения окна
	assert.Eventually(t, func() bool {
		return rl.Allow("key").Allowed
	}, 500*time.Millisecond, 50*time.Millisecond)
}

func TestRateLimiter_DifferentKeys(t *testing.T) {
	rl := middleware.NewRateLimiter(1*time.Second, 1)
	defer rl.Stop()

	assert.True(t, rl.Allow("a").Allowed)
	assert.False(t, rl.Allow("a").Allowed)
	assert.True(t, rl.Allow("b").Allowed)
}

func TestGlobalRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.GlobalRateLimit(100*time.Millisecond, 2))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		req.RemoteAddr = "1.2.3.4:1234"
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	}

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Contains(t, w.Body.String(), middleware.ErrRateLimitGlobal)
}

func TestLoginRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.LoginRateLimit(100*time.Millisecond, 2))
	router.POST("/login", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/login", nil)
		req.RemoteAddr = "5.6.7.8:5678"
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	}

	req := httptest.NewRequest("POST", "/login", nil)
	req.RemoteAddr = "5.6.7.8:5678"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Contains(t, w.Body.String(), middleware.ErrRateLimitLogin)
}

func TestCodeSubmissionRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", uint(10))
		c.Next()
	})
	router.Use(middleware.CodeSubmissionRateLimit(100*time.Millisecond, 2))
	router.POST("/code", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/code", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	}

	req := httptest.NewRequest("POST", "/code", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Contains(t, w.Body.String(), middleware.ErrRateLimitCode)
}

func TestCodeSubmissionRateLimit_NoUserID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.CodeSubmissionRateLimit(100*time.Millisecond, 2))
	router.POST("/code", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("POST", "/code", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	}
}

// =============================================================================
// Тесты для SecurityHeadersMiddleware
// =============================================================================

func TestSecurityHeadersMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.SecurityHeadersMiddleware())
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "strict-origin-when-cross-origin", w.Header().Get("Referrer-Policy"))
	assert.Contains(t, w.Header().Get("Content-Security-Policy"), "default-src 'self'")
}

// =============================================================================
// Тесты для StaticCacheMiddleware
// =============================================================================

func TestStaticCacheMiddleware_StaticPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.StaticCacheMiddleware())
	router.GET("/static/css/style.css", func(c *gin.Context) {
		c.String(http.StatusOK, "css")
	})

	req := httptest.NewRequest("GET", "/static/css/style.css", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "public, max-age=31536000, immutable", w.Header().Get("Cache-Control"))
}

func TestStaticCacheMiddleware_UploadsPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.StaticCacheMiddleware())
	router.GET("/uploads/avatar.png", func(c *gin.Context) {
		c.String(http.StatusOK, "image")
	})

	req := httptest.NewRequest("GET", "/uploads/avatar.png", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "no-cache, must-revalidate", w.Header().Get("Cache-Control"))
}

func TestStaticCacheMiddleware_OtherPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.StaticCacheMiddleware())
	router.GET("/api/data", func(c *gin.Context) {
		c.String(http.StatusOK, "data")
	})

	req := httptest.NewRequest("GET", "/api/data", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("Cache-Control"))
}

// =============================================================================
// Бенчмарки для RateLimiter
// =============================================================================

func BenchmarkRateLimiter_Allow(b *testing.B) {
	rl := middleware.NewRateLimiter(time.Second, 1000)
	defer rl.Stop()
	key := "bench"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rl.Allow(key)
	}
}
