package render

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"testing"

	"gengine-0/internal/pkg/templatefuncs"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// testSessionContext создаёт полноценный gin-роутер с HTML-рендерером и
// sessions-сессией, маршрут которого вызывает Page (как в проде).
func testSessionContext(t *testing.T, tmpl *template.Template, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.SetHTMLTemplate(tmpl)
	store := cookie.NewStore([]byte("test-secret"))
	engine.Use(sessions.Sessions("gengine_session", store))
	engine.GET("/_test_render", handler)

	r := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/_test_render", nil)
	engine.ServeHTTP(r, req)
	return r
}

// M5 (pass 30): tzOffsetFromCookie читает минуты от UTC из cookie tz_offset.
func TestTZOffsetFromCookie_Missing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request, _ = http.NewRequest(http.MethodGet, "/", nil)
	assert.Equal(t, 0, tzOffsetFromCookie(c))
}

func TestTZOffsetFromCookie_Valid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request, _ = http.NewRequest(http.MethodGet, "/", nil)
	c.Request.AddCookie(&http.Cookie{Name: "tz_offset", Value: "180"})
	assert.Equal(t, 180, tzOffsetFromCookie(c))
}

func TestTZOffsetFromCookie_Negative(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request, _ = http.NewRequest(http.MethodGet, "/", nil)
	c.Request.AddCookie(&http.Cookie{Name: "tz_offset", Value: "-300"})
	assert.Equal(t, -300, tzOffsetFromCookie(c))
}

func TestTZOffsetFromCookie_OutOfRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request, _ = http.NewRequest(http.MethodGet, "/", nil)
	c.Request.AddCookie(&http.Cookie{Name: "tz_offset", Value: "841"})
	assert.Equal(t, 0, tzOffsetFromCookie(c))
}

func TestTZOffsetFromCookie_InvalidValue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request, _ = http.NewRequest(http.MethodGet, "/", nil)
	c.Request.AddCookie(&http.Cookie{Name: "tz_offset", Value: "abc"})
	assert.Equal(t, 0, tzOffsetFromCookie(c))
}

// M4 (pass 30): Page рендерит блок ExtraHead в данные layout.
func TestPage_RendersExtraHead(t *testing.T) {
	fm := templatefuncs.FuncMap()
	tmpl := template.Must(template.New("").Funcs(fm).Parse(`
		{{define "layout.html"}}<head>{{.ExtraHead}}</head><body>{{.ContentHTML}}</body>{{end}}
		{{define "ExtraHead"}}<meta property="og:title" content="{{.Title}}">{{end}}
		{{define "content-page.html"}}<h1>Body</h1>{{end}}
	`))
	cleanup := SetTemplateForTest(tmpl)
	defer cleanup()

	r := testSessionContext(t, tmpl, func(c *gin.Context) {
		Page(c, http.StatusOK, "content-page.html", gin.H{"Title": "Hello"})
	})
	assert.Contains(t, r.Body.String(), `<meta property="og:title" content="Hello">`)
	assert.Contains(t, r.Body.String(), "Body")
}

// M4 (pass 30): Page без блока ExtraHead не ломает рендер.
func TestPage_NoExtraHead(t *testing.T) {
	fm := templatefuncs.FuncMap()
	tmpl := template.Must(template.New("").Funcs(fm).Parse(`
		{{define "layout.html"}}<head>{{.ExtraHead}}</head><body>{{.ContentHTML}}</body>{{end}}
		{{define "content-page.html"}}<h1>Body</h1>{{end}}
	`))
	cleanup := SetTemplateForTest(tmpl)
	defer cleanup()

	r := testSessionContext(t, tmpl, func(c *gin.Context) {
		Page(c, http.StatusOK, "content-page.html", gin.H{})
	})
	assert.Contains(t, r.Body.String(), "Body")
}



func TestDefaultErrorMessage(t *testing.T) {
	assert.Equal(t, "Неверный запрос", defaultErrorMessage(http.StatusBadRequest))
	assert.Equal(t, "Доступ запрещён", defaultErrorMessage(http.StatusForbidden))
	assert.Equal(t, "Не найдено", defaultErrorMessage(http.StatusNotFound))
	assert.Equal(t, "Внутренняя ошибка сервера", defaultErrorMessage(http.StatusInternalServerError))
	assert.Equal(t, "Ошибка", defaultErrorMessage(http.StatusTeapot))
}

func TestErrorTemplateForStatus(t *testing.T) {
	assert.Equal(t, "errors-400.html", errorTemplateForStatus(http.StatusBadRequest))
	assert.Equal(t, "errors-403.html", errorTemplateForStatus(http.StatusForbidden))
	assert.Equal(t, "errors-404.html", errorTemplateForStatus(http.StatusNotFound))
	assert.Equal(t, "errors-500.html", errorTemplateForStatus(http.StatusInternalServerError))
	assert.Equal(t, "errors-500.html", errorTemplateForStatus(http.StatusTeapot))
}

func TestParseID_Valid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = []gin.Param{{Key: "id", Value: "42"}}

	id, ok := ParseID(c, "id")
	assert.True(t, ok)
	assert.Equal(t, uint(42), id)
}

func TestParseID_Invalid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = []gin.Param{{Key: "id", Value: "abc"}}

	_, ok := ParseID(c, "id")
	assert.False(t, ok)
}

func TestParseID_Zero(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = []gin.Param{{Key: "id", Value: "0"}}

	_, ok := ParseID(c, "id")
	assert.False(t, ok)
}

func TestParseIDQuery_Valid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/?id=99", nil)

	id, ok := ParseIDQuery(c, "id")
	assert.True(t, ok)
	assert.Equal(t, uint(99), id)
}

func TestParseIDQuery_Invalid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/?id=abc", nil)

	_, ok := ParseIDQuery(c, "id")
	assert.False(t, ok)
}

func TestParseIDQuery_Missing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	_, ok := ParseIDQuery(c, "id")
	assert.False(t, ok)
}

func TestSetBreadcrumb(t *testing.T) {
	data := gin.H{}
	SetBreadcrumb(data, BreadcrumbItem{Name: "Home", URL: "/"})
	assert.Contains(t, data, "Breadcrumbs")
	assert.Len(t, data["Breadcrumbs"], 1)
}

func TestSetBreadcrumb_NilData(t *testing.T) {
	SetBreadcrumb(nil, BreadcrumbItem{Name: "Home"})
	// should not panic
}

func TestSetBreadcrumb_Multiple(t *testing.T) {
	data := gin.H{}
	SetBreadcrumb(data,
		BreadcrumbItem{Name: "Home", URL: "/"},
		BreadcrumbItem{Name: "Profile", URL: "/profile"},
	)
	items, ok := data["Breadcrumbs"].([]map[string]string)
	assert.True(t, ok)
	assert.Len(t, items, 2)
	assert.Equal(t, "Profile", items[1]["name"])
}

func TestPage_NoTemplate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	Page(c, http.StatusOK, "test.html", nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "Template engine not initialized")
}

func TestRenderError_Unauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	RenderErrorPage(c, http.StatusForbidden)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRenderError_WithMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	RenderError(c, http.StatusNotFound, "custom message")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRenderError_EmptyMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	RenderError(c, http.StatusBadRequest, "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
