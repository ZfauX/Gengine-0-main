// internal/pkg/render/htmlcache_test.go
package render

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnonCacheKey(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/games?page=2", nil)
	assert.Equal(t, "/games?page=2|ru", anonCacheKey(c))

	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	c2.Request = httptest.NewRequest("GET", "/", nil)
	assert.Equal(t, "/|ru", anonCacheKey(c2))
}

func TestIsCacheableAnon(t *testing.T) {
	// GET / — аноним, без cookie — кэшируется.
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/", nil)
	assert.True(t, isCacheableAnon(c), "GET / anonymous should be cacheable")

	// POST — нет.
	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	c2.Request = httptest.NewRequest("POST", "/", nil)
	assert.False(t, isCacheableAnon(c2), "POST must not be cached")

	// Не-allowlist путь — нет.
	c3, _ := gin.CreateTestContext(httptest.NewRecorder())
	c3.Request = httptest.NewRequest("GET", "/profile", nil)
	assert.False(t, isCacheableAnon(c3), "non-allowlist path must not be cached")

	// С cookie сессии (авторизованный) — нет.
	c4, _ := gin.CreateTestContext(httptest.NewRecorder())
	c4.Request = httptest.NewRequest("GET", "/", nil)
	c4.Request.AddCookie(&http.Cookie{Name: "gengine_session", Value: "x"})
	assert.False(t, isCacheableAnon(c4), "session cookie must disable caching")
}

func TestPlaceholderReplacements(t *testing.T) {
	// Проверка, что плейсхолдеры уникальны и не пересекаются с реальными значениями.
	nonce := "abc123nonce"
	csrf := "tok123"
	html := "<script nonce=\"GENGINE_CSP_NONCE_PLACEHOLDER_7f3a\">x</script><meta name=\"csrf\" content=\"GENGINE_CSRF_TOKEN_PLACEHOLDER_9b2c\">"
	html = strings.ReplaceAll(html, noncePlaceholder, nonce)
	html = strings.ReplaceAll(html, csrfPlaceholder, csrf)
	assert.Contains(t, html, nonce)
	assert.Contains(t, html, csrf)
	assert.NotContains(t, html, noncePlaceholder)
	assert.NotContains(t, html, csrfPlaceholder)
}

func TestAnonCache_StoreAndServe(t *testing.T) {
	clearAnonCache()
	defer clearAnonCache()

	rec1 := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec1)
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Set("userID", uint(0))
	c.Set("csp_nonce", "nonce1")

	// storeAnonCache: заменяет реальные nonce/CSRF на плейсхолдеры.
	rendered := "<html><script nonce=\"nonce1\">a</script><meta name=\"csrf-token\" content=\"csrf1\">body</html>"
	storeAnonCache(c, gin.H{"csrf": "csrf1"}, []byte(rendered))

	// Второй запрос — другой nonce/CSRF; tryServeAnonCache должен подставить их.
	rec2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(rec2)
	c2.Request = httptest.NewRequest("GET", "/", nil)
	c2.Set("userID", uint(0))
	c2.Set("csp_nonce", "nonce2")
	require.True(t, tryServeAnonCache(c2, http.StatusOK, gin.H{"csrf": "csrf2"}), "cache hit expected")

	body := rec2.Body.String()
	require.Contains(t, body, "nonce2", "fresh nonce must be substituted")
	require.Contains(t, body, "csrf2", "fresh csrf must be substituted")
	require.NotContains(t, body, noncePlaceholder, "no placeholder leaked")
	require.NotContains(t, body, csrfPlaceholder, "no csrf placeholder leaked")
	require.NotContains(t, body, "nonce1", "old nonce must not leak")
}
