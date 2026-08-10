// internal/domain/user/auth_handler_test.go
package user

import (
	"crypto/tls"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"gengine-0/internal/pkg/render"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestIsHTTPS(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name     string
		setTLS   bool
		expected bool
	}{
		{
			name:     "TLS connection",
			setTLS:   true,
			expected: true,
		},
		{
			name:     "HTTP connection",
			setTLS:   false,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.setTLS {
				req.TLS = new(tls.ConnectionState)
			}
			c.Request = req

			result := isHTTPS(c)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestUserIDRequest_Binding(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name        string
		param       string
		expectError bool
	}{
		{
			name:        "valid id",
			param:       "123",
			expectError: false,
		},
		{
			name:        "invalid id",
			param:       "abc",
			expectError: true,
		},
		{
			name:        "zero id",
			param:       "0",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Params = gin.Params{{Key: "id", Value: tt.param}}

			var req UserIDRequest
			err := c.ShouldBindUri(&req)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, uint(123), req.ID)
			}
		})
	}
}

func TestRegisterInput_Validation(t *testing.T) {
	tests := []struct {
		name      string
		input     RegisterInput
		expectErr bool
	}{
		{
			name: "valid input",
			input: RegisterInput{
				Email:    "test@example.com",
				Password: "password123",
				Name:     "Test User",
			},
			expectErr: false,
		},
		{
			name: "invalid email",
			input: RegisterInput{
				Email:    "invalid",
				Password: "password123",
				Name:     "Test User",
			},
			expectErr: true,
		},
		{
			name: "password too short",
			input: RegisterInput{
				Email:    "test@example.com",
				Password: "12345",
				Name:     "Test User",
			},
			expectErr: true,
		},
		{
			name: "accept_terms parses from form",
			input: RegisterInput{
				Email:       "test@example.com",
				Password:    "password123",
				Name:        "Test User",
				AcceptTerms: true,
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			body := url.Values{
				"email":    {tt.input.Email},
				"password": {tt.input.Password},
				"name":     {tt.input.Name},
			}
			if tt.input.AcceptTerms {
				body.Set("accept_terms", "1")
			}
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			c.Request = req
			err := c.ShouldBind(&tt.input)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// ──── 2FA Login Flow Tests ────

func TestTwoFALoginForm_NoPendingSession(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Minimal template needed for the redirect case (not actually rendered)
	tmpl := template.Must(template.New("").Parse(`
{{define "layout.html"}}<html><body>{{.ContentHTML}}</body></html>{{end}}
{{define "auth-login-2fa.html"}}<h1>2FA Login</h1>{{end}}
{{define "errors-404.html"}}<h1>Not Found</h1>{{end}}
`))
	// D6: восстанавливаем глобальный шаблон после теста.
	t.Cleanup(render.SetTemplateForTest(tmpl))

	router := gin.New()
	store := cookie.NewStore([]byte("test-session-secret-32chars-long!!!"))
	router.Use(sessions.Sessions("gengine_test_session", store))

	handler := &AuthHandler{}
	router.GET("/auth/2fa/login", handler.TwoFALoginForm)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/2fa/login", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code, "should redirect to /auth/login without pending session")
	assert.Equal(t, "/auth/login", w.Header().Get("Location"))
}

func TestTwoFALoginForm_WithPendingSession(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tmpl := template.Must(template.New("").Parse(`
{{define "layout.html"}}<html><body>{{.ContentHTML}}</body></html>{{end}}
{{define "auth-login-2fa.html"}}<h1>2FA Login</h1>{{end}}
{{define "errors-404.html"}}<h1>Not Found</h1>{{end}}
`))
	t.Cleanup(render.SetTemplateForTest(tmpl))

	router := gin.New()
	router.SetHTMLTemplate(tmpl)
	store := cookie.NewStore([]byte("test-session-secret-32chars-long!!!"))
	router.Use(sessions.Sessions("gengine_test_session", store))
	router.Use(func(c *gin.Context) {
		c.Set("csrfSecret", "test-csrf-secret-32chars-long!!!")
		c.Set("csrfToken", "test-csrf-token")
		c.Next()
	})

	handler := &AuthHandler{}
	router.GET("/auth/2fa/login", handler.TwoFALoginForm)

	// Устанавливаем pending_user_id через сессию.
	router.GET("/setup-session", func(c *gin.Context) {
		sess := sessions.Default(c)
		sess.Set("pending_user_id", uint(42))
		if err := sess.Save(); err != nil {
			t.Fatal(err)
		}
		c.Status(http.StatusOK)
	})

	w0 := httptest.NewRecorder()
	req0 := httptest.NewRequest(http.MethodGet, "/setup-session", nil)
	router.ServeHTTP(w0, req0)

	// Новый запрос с сохранённой сессией.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/2fa/login", nil)
	for _, ck := range w0.Result().Cookies() {
		req.AddCookie(ck)
	}
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "form should render with pending session")
	assert.Contains(t, w.Body.String(), "2FA Login")
}

func TestTwoFALoginVerify_NoPendingSession(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Templates not needed — handler redirects before rendering
	router := gin.New()
	store := cookie.NewStore([]byte("test-session-secret-32chars-long!!!"))
	router.Use(sessions.Sessions("gengine_test_session", store))

	handler := &AuthHandler{}
	router.POST("/auth/2fa/login", handler.TwoFALoginVerify)

	w := httptest.NewRecorder()
	body := url.Values{"code": {"123456"}}.Encode()
	req := httptest.NewRequest(http.MethodPost, "/auth/2fa/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code, "should redirect to /auth/login without pending session")
	assert.Equal(t, "/auth/login", w.Header().Get("Location"))
}

func TestTwoFALoginVerify_InvalidCode(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tmpl := template.Must(template.New("").Parse(`
{{define "layout.html"}}<html><body>{{.ContentHTML}}</body></html>{{end}}
{{define "auth-login-2fa.html"}}<h1>2FA Login</h1>{{if .Error}}<p class="error">{{.Error}}</p>{{end}}{{end}}
{{define "errors-404.html"}}<h1>Not Found</h1>{{end}}
`))
	t.Cleanup(render.SetTemplateForTest(tmpl))

	router := gin.New()
	router.SetHTMLTemplate(tmpl)
	store := cookie.NewStore([]byte("test-session-secret-32chars-long!!!"))
	router.Use(sessions.Sessions("gengine_test_session", store))
	router.Use(func(c *gin.Context) {
		c.Set("csrfSecret", "test-csrf-secret-32chars-long!!!")
		c.Set("csrfToken", "test-csrf-token")
		c.Next()
	})

	handler := &AuthHandler{twoFactorSvc: NewTwoFactorService()}
	router.POST("/auth/2fa/login", handler.TwoFALoginVerify)

	// Устанавливаем pending_user_id + pending_expires (S-3, pass 37: без
	// expires fail-closed редиректит на /auth/login).
	router.GET("/setup-session", func(c *gin.Context) {
		sess := sessions.Default(c)
		sess.Set("pending_user_id", uint(42))
		sess.Set("pending_expires", time.Now().Add(pending2FATTL).Unix())
		if err := sess.Save(); err != nil {
			t.Fatal(err)
		}
		c.Status(http.StatusOK)
	})

	w0 := httptest.NewRecorder()
	req0 := httptest.NewRequest(http.MethodGet, "/setup-session", nil)
	router.ServeHTTP(w0, req0)

	w := httptest.NewRecorder()
	body := url.Values{"code": {"12"}}.Encode() // слишком короткий
	req := httptest.NewRequest(http.MethodPost, "/auth/2fa/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, ck := range w0.Result().Cookies() {
		req.AddCookie(ck)
	}
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "should render error for invalid code format")
	assert.Contains(t, w.Body.String(), "2FA Login")
}

// A-8 (pass 31): TestTwoFALoginVerify_ValidCode удалён — это была пустая
// заглушка t.Skip. Покрытие 2FA-логина обеспечивается интеграционными
// тестами двухфакторной аутентификации (two_factor_service_test.go) и
// новым TestTwoFALoginVerify_Lockout (unit с моками).
func TestLoginInput_Validation(t *testing.T) {
	tests := []struct {
		name      string
		input     LoginInput
		expectErr bool
	}{
		{
			name: "valid input",
			input: LoginInput{
				Email:    "test@example.com",
				Password: "password123",
			},
			expectErr: false,
		},
		{
			name: "invalid email",
			input: LoginInput{
				Email:    "invalid",
				Password: "password123",
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			body := url.Values{
				"email":    {tt.input.Email},
				"password": {tt.input.Password},
			}.Encode()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			c.Request = req
			err := c.ShouldBind(&tt.input)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
