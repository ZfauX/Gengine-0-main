package render

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

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
	assert.Contains(t, data, "Breadcrumb")
	assert.Len(t, data["Breadcrumb"], 1)
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
	items := data["Breadcrumb"].([]BreadcrumbItem)
	assert.Len(t, items, 2)
	assert.Equal(t, "Profile", items[1].Name)
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
