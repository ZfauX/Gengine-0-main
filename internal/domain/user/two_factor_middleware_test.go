// internal/domain/user/two_factor_middleware_test.go
package user

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
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

func (m *mockUserRepo) GetByIDWithAchievementsAndSubscriptions(ctx context.Context, id uint) (*User, error) {
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

func (m *mockUserRepo) CountSearch(ctx context.Context, query, role string) (int64, error) {
	return 0, nil
}

func (m *mockUserRepo) SearchPaginated(ctx context.Context, query, role string, offset, limit int) ([]User, error) {
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

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "/auth/2fa/verify")
	assert.Contains(t, w.Header().Get("Location"), "return_url=%2Ftest")
}

func TestTwoFactorRequired_RedirectWithQueryPreserved(t *testing.T) {
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
	req := httptest.NewRequest(http.MethodGet, "/test?foo=bar", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "return_url=%2Ftest%3Ffoo%3Dbar")
}

func TestTwoFactorRequired_AlreadyVerifiedInSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := NewTwoFactorService()
	userRepo := &mockUserRepo{
		users: map[uint]*User{
			1: {Model: gorm.Model{ID: 1}, TwoFactorEnabled: true},
		},
	}

	// Создаём router с middleware, который ставит флаг в сессию
	// с middleware, который ставит флаг в сессию
	store := cookie.NewStore([]byte("test-session-secret-32chars-long!!!"))
	tmpl := template.Must(template.New("").Parse(`{{define "layout.html"}}<html><body>{{.ContentHTML}}</body></html>{{end}}`))
	render.SetTemplate(tmpl)

	router2 := gin.New()
	router2.SetHTMLTemplate(tmpl)
	router2.Use(sessions.Sessions("gengine_test_session", store))
	router2.Use(func(c *gin.Context) {
		c.Set("userID", uint(1))
		// Ставим флаг в сессию до middleware (ключ привязан к userID)
		sess := sessions.Default(c)
		sess.Set(session2FAKey(1), true)
		sess.Save()
		c.Next()
	})
	router2.Use(TwoFactorRequired(svc, userRepo))
	router2.Any("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "OK")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	router2.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "OK", w.Body.String())

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "OK", w.Body.String())
}

func TestTwoFactorRequired_InvalidUserIDType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := NewTwoFactorService()
	userRepo := &mockUserRepo{users: make(map[uint]*User)}

	tmpl := template.Must(template.New("").Parse(`
{{define "layout.html"}}<html><body>{{.ContentHTML}}</body></html>{{end}}
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

func TestTwoFactorBackupCodeRequired_RedirectToBackupPage(t *testing.T) {
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

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "/auth/2fa/backup")
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
	userRepo := &mockUserRepo{
		users: map[uint]*User{
			1: {Model: gorm.Model{ID: 1}, TwoFactorEnabled: true},
		},
	}

	store := cookie.NewStore([]byte("test-session-secret-32chars-long!!!"))
	tmpl := template.Must(template.New("").Parse(`{{define "layout.html"}}<html><body>{{.ContentHTML}}</body></html>{{end}}`))
	render.SetTemplate(tmpl)

	router := gin.New()
	router.SetHTMLTemplate(tmpl)
	router.Use(sessions.Sessions("gengine_test_session", store))
	router.Use(func(c *gin.Context) {
		c.Set("userID", uint(1))
		sess := sessions.Default(c)
		sess.Set(session2FAKey(1), true)
		sess.Save()
		c.Next()
	})
	router.Use(TwoFactorBackupCodeRequired(svc, userRepo))
	router.Any("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "OK")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "OK", w.Body.String())
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
