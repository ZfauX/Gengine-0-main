// internal/domain/user/two_factor_middleware_test.go
package user

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"gengine-0/internal/pkg/render"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// mockUserRepo — минимальный мок UserRepository для тестов middleware.
type mockUserRepo struct {
	users map[uint]*User
}

func (m *mockUserRepo) Create(ctx context.Context, user *User) error {
	m.users[user.ID] = user
	return nil
}

func (m *mockUserRepo) GetByID(ctx context.Context, id uint) (*User, error) {
	u, ok := m.users[id]
	if !ok {
		return nil, assert.AnError
	}
	return u, nil
}

func (m *mockUserRepo) GetByEmail(ctx context.Context, email string) (*User, error) {
	return nil, nil
}

func (m *mockUserRepo) GetPublicProfile(ctx context.Context, id uint) (*User, error) {
	return nil, nil
}

func (m *mockUserRepo) Update(ctx context.Context, id uint, fields map[string]any) error {
	return nil
}

func (m *mockUserRepo) GetByRole(ctx context.Context, role string) ([]User, error) {
	return nil, nil
}

func (m *mockUserRepo) GetUserRole(ctx context.Context, id uint) (string, error) {
	return "", nil
}

func (m *mockUserRepo) Count(ctx context.Context) (int64, error) {
	return 0, nil
}

func (m *mockUserRepo) CountByRole(ctx context.Context, role string) (int64, error) {
	return 0, nil
}

func (m *mockUserRepo) List(ctx context.Context, role string) ([]User, error) {
	return nil, nil
}

func (m *mockUserRepo) ListPaginated(ctx context.Context, role string, offset, limit int) ([]User, error) {
	return nil, nil
}

func (m *mockUserRepo) Delete(ctx context.Context, id uint) error {
	return nil
}

func (m *mockUserRepo) AtomicIncrementFailedAttempts(ctx context.Context, userID uint) (int, error) {
	return 0, nil
}

// newTwoFactorTestRouter создаёт gin.Engine с сессиями и опциональной 2FA middleware.
func newTwoFactorTestRouter(t *testing.T, middleware gin.HandlerFunc, userID uint) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	tmpl := template.Must(template.New("").Parse(`
{{define "layout.html"}}<html><body>{{.ContentHTML}}</body></html>{{end}}
{{define "admin-2fa-verify.html"}}<h1>2FA Verify</h1>{{if .Error}}<p class="error">{{.Error}}</p>{{end}}<form>{{if .Message}}<p>{{.Message}}</p>{{end}}{{.ReturnURL}}</form>{{end}}
{{define "admin-2fa-backup.html"}}<h1>2FA Backup</h1>{{if .Error}}<p class="error">{{.Error}}</p>{{end}}<form><input name="backup_code"></form>{{end}}
`))
	render.SetTemplate(tmpl)

	router := gin.New()
	router.SetHTMLTemplate(tmpl)

	store := cookie.NewStore([]byte("test-session-secret-32chars-long!!!"))
	router.Use(sessions.Sessions("gengine_test_session", store))

	// Устанавливаем userID в контексте
	router.Use(func(c *gin.Context) {
		c.Set("userID", userID)
		c.Next()
	})

	if middleware != nil {
		router.Use(middleware)
	}

	router.Any("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "OK")
	})

	return router
}

// --- TwoFactorRequired ---

func TestTwoFactorRequired_SkipWhenNoUserID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := NewTwoFactorService()
	userRepo := &mockUserRepo{users: make(map[uint]*User)}

	tmpl := template.Must(template.New("").Parse(`
{{define "layout.html"}}<html><body>{{.ContentHTML}}</body></html>{{end}}
{{define "admin-2fa-verify.html"}}<h1>2FA Verify</h1>{{end}}
{{define "admin-2fa-backup.html"}}<h1>2FA Backup</h1>{{end}}
`))
	render.SetTemplate(tmpl)

	router := gin.New()
	router.SetHTMLTemplate(tmpl)

	store := cookie.NewStore([]byte("test-session-secret-32chars-long!!!"))
	router.Use(sessions.Sessions("gengine_test_session", store))
	// Не устанавливаем userID — middleware должен вернуть 401
	router.Use(TwoFactorRequired(svc, userRepo))
	router.Any("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "OK")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestTwoFactorRequired_SkipWhen2FADisabled(t *testing.T) {
	svc := NewTwoFactorService()
	userRepo := &mockUserRepo{
		users: map[uint]*User{
			1: {Model: gorm.Model{ID: 1}, TwoFactorEnabled: false},
		},
	}
	router := newTwoFactorTestRouter(t, TwoFactorRequired(svc, userRepo), 1)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "OK", w.Body.String())
}

func TestTwoFactorRequired_RedirectToVerifyWhenNoCode(t *testing.T) {
	svc := NewTwoFactorService()
	secret, err := svc.GenerateSecret()
	require.NoError(t, err)

	userRepo := &mockUserRepo{
		users: map[uint]*User{
			1: {
				Model:            gorm.Model{ID: 1},
				TwoFactorEnabled: true,
				TwoFactorSecret:  secret,
			},
		},
	}
	router := newTwoFactorTestRouter(t, TwoFactorRequired(svc, userRepo), 1)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "2FA Verify")
	assert.Contains(t, w.Body.String(), "Введите код из Google Authenticator")
}

func TestTwoFactorRequired_InvalidCode(t *testing.T) {
	svc := NewTwoFactorService()
	secret, err := svc.GenerateSecret()
	require.NoError(t, err)

	userRepo := &mockUserRepo{
		users: map[uint]*User{
			1: {
				Model:            gorm.Model{ID: 1},
				TwoFactorEnabled: true,
				TwoFactorSecret:  secret,
			},
		},
	}
	router := newTwoFactorTestRouter(t, TwoFactorRequired(svc, userRepo), 1)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test?code=000000", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Неверный код")
}

func TestTwoFactorRequired_ValidCode(t *testing.T) {
	svc := NewTwoFactorService()
	secret, err := svc.GenerateSecret()
	require.NoError(t, err)

	validCode, err := svc.GenerateTOTPCode(secret)
	require.NoError(t, err)

	userRepo := &mockUserRepo{
		users: map[uint]*User{
			1: {
				Model:            gorm.Model{ID: 1},
				TwoFactorEnabled: true,
				TwoFactorSecret:  secret,
			},
		},
	}
	router := newTwoFactorTestRouter(t, TwoFactorRequired(svc, userRepo), 1)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test?code="+validCode, nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "OK", w.Body.String())
}

func TestTwoFactorRequired_AlreadyVerifiedInSession(t *testing.T) {
	svc := NewTwoFactorService()
	secret, err := svc.GenerateSecret()
	require.NoError(t, err)

	userRepo := &mockUserRepo{
		users: map[uint]*User{
			1: {
				Model:            gorm.Model{ID: 1},
				TwoFactorEnabled: true,
				TwoFactorSecret:  secret,
			},
		},
	}
	router := newTwoFactorTestRouter(t, TwoFactorRequired(svc, userRepo), 1)

	// Первый запрос: устанавливаем флаг верификации в сессии через валидный код
	validCode, err := svc.GenerateTOTPCode(secret)
	require.NoError(t, err)
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/test?code="+validCode, nil)
	router.ServeHTTP(w1, req1)

	// Второй запрос с той же сессией — middleware должен пропустить
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	for _, ck := range w1.Result().Cookies() {
		req2.AddCookie(ck)
	}
	router.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, "OK", w2.Body.String())
}

func TestTwoFactorRequired_InvalidUserIDType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := NewTwoFactorService()
	userRepo := &mockUserRepo{users: make(map[uint]*User)}

	tmpl := template.Must(template.New("").Parse(`
{{define "layout.html"}}<html><body>{{.ContentHTML}}</body></html>{{end}}
{{define "admin-2fa-verify.html"}}<h1>2FA Verify</h1>{{end}}
{{define "admin-2fa-backup.html"}}<h1>2FA Backup</h1>{{end}}
`))
	render.SetTemplate(tmpl)

	router := gin.New()
	router.SetHTMLTemplate(tmpl)

	store := cookie.NewStore([]byte("test-session-secret-32chars-long!!!"))
	router.Use(sessions.Sessions("gengine_test_session", store))
	// Устанавливаем userID неверного типа
	router.Use(func(c *gin.Context) {
		c.Set("userID", "not-a-uint")
		c.Next()
	})
	router.Use(TwoFactorRequired(svc, userRepo))
	router.Any("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "OK")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// --- TwoFactorBackupCodeRequired ---

func TestTwoFactorBackupCodeRequired_NoUserID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := NewTwoFactorService()
	userRepo := &mockUserRepo{users: make(map[uint]*User)}

	tmpl := template.Must(template.New("").Parse(`
{{define "layout.html"}}<html><body>{{.ContentHTML}}</body></html>{{end}}
{{define "admin-2fa-verify.html"}}<h1>2FA Verify</h1>{{end}}
{{define "admin-2fa-backup.html"}}<h1>2FA Backup</h1>{{if .Error}}<p class="error">{{.Error}}</p>{{end}}{{end}}
`))
	render.SetTemplate(tmpl)

	router := gin.New()
	router.SetHTMLTemplate(tmpl)

	store := cookie.NewStore([]byte("test-session-secret-32chars-long!!!"))
	router.Use(sessions.Sessions("gengine_test_session", store))
	// Не устанавливаем userID
	router.Use(TwoFactorBackupCodeRequired(svc, userRepo))
	router.Any("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "OK")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestTwoFactorBackupCodeRequired_NoCode(t *testing.T) {
	svc := NewTwoFactorService()
	userRepo := &mockUserRepo{
		users: map[uint]*User{
			1: {Model: gorm.Model{ID: 1}, TwoFactorEnabled: true},
		},
	}
	router := newTwoFactorTestRouter(t, TwoFactorBackupCodeRequired(svc, userRepo), 1)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Введите резервный код")
}

func TestTwoFactorBackupCodeRequired_InvalidBackupCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := NewTwoFactorService()

	codes, err := svc.GenerateBackupCodes()
	require.NoError(t, err)
	hashedCodes, err := svc.HashBackupCodes(codes)
	require.NoError(t, err)

	userRepo := &mockUserRepo{
		users: map[uint]*User{
			1: {
				Model:                gorm.Model{ID: 1},
				TwoFactorEnabled:     true,
				TwoFactorBackupCodes: hashedCodes,
			},
		},
	}

	tmpl := template.Must(template.New("").Parse(`
{{define "layout.html"}}<html><body>{{.ContentHTML}}</body></html>{{end}}
{{define "admin-2fa-verify.html"}}<h1>2FA Verify</h1>{{end}}
{{define "admin-2fa-backup.html"}}<h1>2FA Backup</h1>{{if .Error}}<p class="error">{{.Error}}</p>{{end}}{{end}}
`))
	render.SetTemplate(tmpl)

	router := gin.New()
	router.SetHTMLTemplate(tmpl)

	store := cookie.NewStore([]byte("test-session-secret-32chars-long!!!"))
	router.Use(sessions.Sessions("gengine_test_session", store))
	router.Use(func(c *gin.Context) {
		c.Set("userID", uint(1))
		c.Next()
	})
	router.Use(TwoFactorBackupCodeRequired(svc, userRepo))
	router.Any("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "OK")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test?backup_code=999999", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Неверный резервный код")
}

func TestTwoFactorBackupCodeRequired_ValidBackupCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := NewTwoFactorService()

	codes, err := svc.GenerateBackupCodes()
	require.NoError(t, err)
	hashedCodes, err := svc.HashBackupCodes(codes)
	require.NoError(t, err)

	userRepo := &mockUserRepo{
		users: map[uint]*User{
			1: {
				Model:                gorm.Model{ID: 1},
				TwoFactorEnabled:     true,
				TwoFactorBackupCodes: hashedCodes,
			},
		},
	}

	tmpl := template.Must(template.New("").Parse(`
{{define "layout.html"}}<html><body>{{.ContentHTML}}</body></html>{{end}}
{{define "admin-2fa-verify.html"}}<h1>2FA Verify</h1>{{end}}
{{define "admin-2fa-backup.html"}}<h1>2FA Backup</h1>{{if .Error}}<p class="error">{{.Error}}</p>{{end}}{{end}}
`))
	render.SetTemplate(tmpl)

	router := gin.New()
	router.SetHTMLTemplate(tmpl)

	store := cookie.NewStore([]byte("test-session-secret-32chars-long!!!"))
	router.Use(sessions.Sessions("gengine_test_session", store))
	router.Use(func(c *gin.Context) {
		c.Set("userID", uint(1))
		c.Next()
	})
	router.Use(TwoFactorBackupCodeRequired(svc, userRepo))
	router.Any("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "OK")
	})

	validCode := codes[0]
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test?backup_code="+validCode, nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "OK", w.Body.String())
}

func TestTwoFactorBackupCodeRequired_SkipWhen2FADisabled(t *testing.T) {
	svc := NewTwoFactorService()
	userRepo := &mockUserRepo{
		users: map[uint]*User{
			1: {Model: gorm.Model{ID: 1}, TwoFactorEnabled: false},
		},
	}
	router := newTwoFactorTestRouter(t, TwoFactorBackupCodeRequired(svc, userRepo), 1)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "OK", w.Body.String())
}

func TestTwoFactorBackupCodeRequired_AlreadyVerifiedInSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := NewTwoFactorService()

	codes, err := svc.GenerateBackupCodes()
	require.NoError(t, err)
	hashedCodes, err := svc.HashBackupCodes(codes)
	require.NoError(t, err)

	userRepo := &mockUserRepo{
		users: map[uint]*User{
			1: {
				Model:                gorm.Model{ID: 1},
				TwoFactorEnabled:     true,
				TwoFactorBackupCodes: hashedCodes,
			},
		},
	}

	tmpl := template.Must(template.New("").Parse(`
{{define "layout.html"}}<html><body>{{.ContentHTML}}</body></html>{{end}}
{{define "admin-2fa-verify.html"}}<h1>2FA Verify</h1>{{end}}
{{define "admin-2fa-backup.html"}}<h1>2FA Backup</h1>{{if .Error}}<p class="error">{{.Error}}</p>{{end}}{{end}}
`))
	render.SetTemplate(tmpl)

	router := gin.New()
	router.SetHTMLTemplate(tmpl)

	store := cookie.NewStore([]byte("test-session-secret-32chars-long!!!"))
	router.Use(sessions.Sessions("gengine_test_session", store))
	router.Use(func(c *gin.Context) {
		c.Set("userID", uint(1))
		c.Next()
	})
	router.Use(TwoFactorBackupCodeRequired(svc, userRepo))
	router.Any("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "OK")
	})

	// Первый запрос: проходим верификацию
	validCode := codes[0]
	form := url.Values{"backup_code": {validCode}}
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(form.Encode()))
	req1.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	router.ServeHTTP(w1, req1)

	// Второй запрос с той же сессией — пропускается
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	for _, ck := range w1.Result().Cookies() {
		req2.AddCookie(ck)
	}
	router.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, "OK", w2.Body.String())
}

func TestTwoFactorBackupCodeRequired_InvalidUserIDType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := NewTwoFactorService()
	userRepo := &mockUserRepo{users: make(map[uint]*User)}

	tmpl := template.Must(template.New("").Parse(`
{{define "layout.html"}}<html><body>{{.ContentHTML}}</body></html>{{end}}
{{define "admin-2fa-verify.html"}}<h1>2FA Verify</h1>{{end}}
{{define "admin-2fa-backup.html"}}<h1>2FA Backup</h1>{{if .Error}}<p class="error">{{.Error}}</p>{{end}}{{end}}
`))
	render.SetTemplate(tmpl)

	router := gin.New()
	router.SetHTMLTemplate(tmpl)

	store := cookie.NewStore([]byte("test-session-secret-32chars-long!!!"))
	router.Use(sessions.Sessions("gengine_test_session", store))
	router.Use(func(c *gin.Context) {
		c.Set("userID", "not-a-uint")
		c.Next()
	})
	router.Use(TwoFactorBackupCodeRequired(svc, userRepo))
	router.Any("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "OK")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// --- withRedirectFlag ---

func TestWithRedirectFlag(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "URL without query params",
			input:    "/admin/users",
			expected: "/admin/users?redirect=1",
		},
		{
			name:     "URL with query params",
			input:    "/admin/users?page=1",
			expected: "/admin/users?page=1&redirect=1",
		},
		{
			name:     "empty URL",
			input:    "",
			expected: "?redirect=1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := withRedirectFlag(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
