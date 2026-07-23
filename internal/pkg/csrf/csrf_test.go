package csrf

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	gocsrf "github.com/gorilla/csrf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSecret = "test-secret-key-32-bytes-long!!"

// plaintextMiddleware marks requests as plaintext HTTP, bypassing the
// origin/referer checks that gorilla/csrf would otherwise enforce.
// This is needed because httptest.Server requests lack a populated r.URL.Host.
func plaintextMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request = gocsrf.PlaintextHTTPRequest(c.Request)
		c.Next()
	}
}

func setupHandler() http.Handler {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(plaintextMiddleware())
	r.Use(Middleware(testSecret, false, nil))
	r.GET("/token", func(c *gin.Context) {
		c.String(http.StatusOK, GetToken(c))
	})
	r.POST("/submit", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	return r
}

func TestMiddleware_GET_ReturnsToken(t *testing.T) {
	s := httptest.NewServer(setupHandler())
	defer s.Close()

	resp, err := s.Client().Get(s.URL + "/token")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, string(body))
}

func TestMiddleware_GET_SetsCookie(t *testing.T) {
	s := httptest.NewServer(setupHandler())
	defer s.Close()

	resp, err := s.Client().Get(s.URL + "/token")
	require.NoError(t, err)
	defer resp.Body.Close()

	found := false
	for _, c := range resp.Cookies() {
		if c.Name == "_csrf_token" {
			found = true
			assert.True(t, c.HttpOnly)
			break
		}
	}
	assert.True(t, found, "CSRF cookie should be set on GET")
}

func TestMiddleware_POST_ValidToken(t *testing.T) {
	s := httptest.NewServer(setupHandler())
	defer s.Close()

	getResp, err := s.Client().Get(s.URL + "/token")
	require.NoError(t, err)
	defer getResp.Body.Close()

	token, _ := io.ReadAll(getResp.Body)
	require.NotEmpty(t, string(token))

	cookies := getResp.Cookies()
	require.NotEmpty(t, cookies)

	form := url.Values{"_csrf": {string(token)}}
	req, _ := http.NewRequest("POST", s.URL+"/submit", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", s.URL+"/token")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := s.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", string(body))
	require.Equal(t, "ok", string(body))
}

func TestMiddleware_POST_MissingToken(t *testing.T) {
	s := httptest.NewServer(setupHandler())
	defer s.Close()

	req, _ := http.NewRequest("POST", s.URL+"/submit", nil)
	req.Header.Set("Referer", s.URL+"/token")
	resp, err := s.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "CSRF token mismatch")
}

func TestMiddleware_POST_WrongToken(t *testing.T) {
	s := httptest.NewServer(setupHandler())
	defer s.Close()

	getResp, err := s.Client().Get(s.URL + "/token")
	require.NoError(t, err)
	defer getResp.Body.Close()

	cookies := getResp.Cookies()

	form := url.Values{"_csrf": {"invalid-token"}}
	req, _ := http.NewRequest("POST", s.URL+"/submit", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", s.URL+"/token")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := s.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "CSRF token mismatch")
}

func TestMiddleware_POST_TokenInHeader(t *testing.T) {
	s := httptest.NewServer(setupHandler())
	defer s.Close()

	getResp, err := s.Client().Get(s.URL + "/token")
	require.NoError(t, err)
	defer getResp.Body.Close()

	token, _ := io.ReadAll(getResp.Body)
	require.NotEmpty(t, string(token))
	cookies := getResp.Cookies()

	req, _ := http.NewRequest("POST", s.URL+"/submit", nil)
	req.Header.Set("X-CSRF-Token", string(token))
	req.Header.Set("Referer", s.URL+"/token")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := s.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, "ok", string(body))
}

func TestMiddleware_DifferentSecrets(t *testing.T) {
	s1 := httptest.NewServer(setupHandler())
	defer s1.Close()

	getResp, err := s1.Client().Get(s1.URL + "/token")
	require.NoError(t, err)
	defer getResp.Body.Close()

	token, _ := io.ReadAll(getResp.Body)
	require.NotEmpty(t, string(token))
	cookies := getResp.Cookies()

	gin.SetMode(gin.TestMode)
	r2 := gin.New()
	r2.Use(Middleware("different-secret-key-32-bytes-long!!", false, nil))
	r2.POST("/submit", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	s2 := httptest.NewServer(r2)
	defer s2.Close()

	form := url.Values{"_csrf": {string(token)}}
	req, _ := http.NewRequest("POST", s2.URL+"/submit", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", s2.URL+"/token")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := s2.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestMiddleware_POST_ExpiredCookie(t *testing.T) {
	s := httptest.NewServer(setupHandler())
	defer s.Close()

	getResp, err := s.Client().Get(s.URL + "/token")
	require.NoError(t, err)
	defer getResp.Body.Close()

	token, _ := io.ReadAll(getResp.Body)
	require.NotEmpty(t, string(token))

	req, _ := http.NewRequest("POST", s.URL+"/submit", nil)
	req.Header.Set("X-CSRF-Token", string(token))
	req.Header.Set("Referer", s.URL+"/token")
	resp, err := s.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "CSRF token mismatch")
}

func TestGetToken_BeforeMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	r.GET("/test", func(c *gin.Context) {
		token := GetToken(c)
		assert.Empty(t, token)
		c.String(http.StatusOK, "")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestMiddleware_SafeMethods(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Middleware(testSecret, false, nil))
	r.GET("/safe", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/safe", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}
