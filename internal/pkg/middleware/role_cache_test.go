// internal/pkg/middleware/role_cache_test.go
// Тесты TTL-кэша ролей (M6, pass 30) через публичный API: SetRoleProvider,
// InvalidateRoleCache и AuthRequired.
package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"gengine-0/internal/pkg/middleware"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetRoleCache восстанавливает глобальное состояние roleProvider/кэша.
func resetRoleCache() {
	middleware.SetRoleProvider(nil)
	middleware.ResetRoleCacheForTest()
}

func TestGetCachedRole_CacheHit(t *testing.T) {
	t.Cleanup(resetRoleCache)

	var calls int64
	middleware.SetRoleProvider(func(ctx context.Context, userID uint) (string, error) {
		atomic.AddInt64(&calls, 1)
		return "admin", nil
	})

	// Первый вызов — в провайдер, второй — из кэша.
	role, err := middleware.GetRoleForTest(context.Background(), 42)
	require.NoError(t, err)
	assert.Equal(t, "admin", role)
	role, err = middleware.GetRoleForTest(context.Background(), 42)
	require.NoError(t, err)
	assert.Equal(t, "admin", role)
	assert.Equal(t, int64(1), atomic.LoadInt64(&calls), "провайдер должен вызваться один раз")
}

func TestGetCachedRole_ErrorsNotCached(t *testing.T) {
	t.Cleanup(resetRoleCache)

	var calls int64
	middleware.SetRoleProvider(func(ctx context.Context, userID uint) (string, error) {
		atomic.AddInt64(&calls, 1)
		if atomic.LoadInt64(&calls) == 1 {
			return "", middleware.ErrTokenUserNotFound
		}
		return "admin", nil
	})

	_, err := middleware.GetRoleForTest(context.Background(), 7)
	require.ErrorIs(t, err, middleware.ErrTokenUserNotFound)
	// Ошибка не кэшируется: следующий вызов снова обращается к провайдеру.
	role, err := middleware.GetRoleForTest(context.Background(), 7)
	require.NoError(t, err)
	assert.Equal(t, "admin", role)
	assert.Equal(t, int64(2), atomic.LoadInt64(&calls))
}

func TestInvalidateRoleCache(t *testing.T) {
	t.Cleanup(resetRoleCache)

	var calls int64
	middleware.SetRoleProvider(func(ctx context.Context, userID uint) (string, error) {
		atomic.AddInt64(&calls, 1)
		return "user", nil
	})

	_, err := middleware.GetRoleForTest(context.Background(), 99)
	require.NoError(t, err)
	_, err = middleware.GetRoleForTest(context.Background(), 99)
	require.NoError(t, err)
	assert.Equal(t, int64(1), atomic.LoadInt64(&calls))

	middleware.InvalidateRoleCache(99)
	_, err = middleware.GetRoleForTest(context.Background(), 99)
	require.NoError(t, err)
	assert.Equal(t, int64(2), atomic.LoadInt64(&calls), "после инвалидации провайдер вызывается снова")
}

func TestGetCachedRole_NoProvider(t *testing.T) {
	t.Cleanup(resetRoleCache)
	role, err := middleware.GetRoleForTest(context.Background(), 1)
	require.NoError(t, err)
	assert.Equal(t, "", role)
}

func TestAuthRequired_UsesRoleProvider(t *testing.T) {
	t.Cleanup(resetRoleCache)

	// Сначала roleProvider не установлен — роль из JWT.
	t.Run("no_provider_uses_jwt_role", func(t *testing.T) {
		resetRoleCache()
		gin.SetMode(gin.TestMode)
		r := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(r)
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: "jwt", Value: "tok"})
		c.Request = req

		parser := &stubTokenParser{parseResult: 5, role: "admin", parseError: nil}
		nextCalled := false
		middleware.AuthRequired(parser)(c)
		if !c.IsAborted() {
			nextCalled = true
		}
		// Без провайдера роль из JWT-claims сохраняется.
		assert.False(t, c.IsAborted(), "запрос не должен быть прерван")
		_ = nextCalled
		assert.Equal(t, "admin", c.GetString("role"))
	})

	// С провайдером роль перечитывается из БД.
	t.Run("with_provider_reads_from_provider", func(t *testing.T) {
		resetRoleCache()
		middleware.SetRoleProvider(func(ctx context.Context, userID uint) (string, error) {
			return "user", nil
		})

		r := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(r)
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: "jwt", Value: "tok"})
		c.Request = req

		parser := &stubTokenParser{parseResult: 5, role: "admin", parseError: nil}
		middleware.AuthRequired(parser)(c)
		assert.False(t, c.IsAborted())
		assert.Equal(t, "user", c.GetString("role"), "роль должна прийти из провайдера, а не из JWT")
	})

	// Удалённый пользователь — ErrTokenUserNotFound → редирект на логин.
	t.Run("deleted_user_redirects_to_login", func(t *testing.T) {
		resetRoleCache()
		middleware.SetRoleProvider(func(ctx context.Context, userID uint) (string, error) {
			return "", middleware.ErrTokenUserNotFound
		})

		r := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(r)
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: "jwt", Value: "tok"})
		c.Request = req

		parser := &stubTokenParser{parseResult: 5, role: "admin", parseError: nil}
		middleware.AuthRequired(parser)(c)
		assert.True(t, c.IsAborted())
		assert.Equal(t, http.StatusFound, r.Code)
	})
}

// TestRoleCache_Expiry: роль с TTL протухает и провайдер вызывается повторно.
func TestRoleCache_Expiry(t *testing.T) {
	t.Cleanup(resetRoleCache)

	var calls int64
	middleware.SetRoleProvider(func(ctx context.Context, userID uint) (string, error) {
		atomic.AddInt64(&calls, 1)
		return "admin", nil
	})

	_, err := middleware.GetRoleForTest(context.Background(), 10)
	require.NoError(t, err)
	// Сдвигаем время кэша на TTL вперёд (тестовая настройка).
	middleware.ExpireRoleCacheForTest()
	_, err = middleware.GetRoleForTest(context.Background(), 10)
	require.NoError(t, err)
	assert.Equal(t, int64(2), atomic.LoadInt64(&calls), "после истечения TTL провайдер вызывается снова")
}
