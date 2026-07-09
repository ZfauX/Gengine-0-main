// internal/pkg/render/helper.go
package render

import (
	"bytes"
	"html/template"
	"net/http"
	"strconv"

	"gengine-0/internal/pkg/middleware"

	"github.com/gin-gonic/gin"
)

var globalTemplate *template.Template

// SetTemplate сохраняет общий *template.Template для использования в хелпере.
func SetTemplate(t *template.Template) {
	globalTemplate = t
}

// Page рендерит указанный подшаблон в буфер, вставляет результат как ContentHTML в layout.html.
// contentTemplate — имя шаблона (например "auth-login.html"), которое должно быть определено в общем наборе.
func Page(c *gin.Context, status int, contentTemplate string, data gin.H) {
	if data == nil {
		data = gin.H{}
	}

	if globalTemplate == nil {
		c.String(http.StatusInternalServerError, "Template engine not initialized")
		return
	}

	// Добавляем nonce в данные шаблона
	nonce := middleware.GetCSPNonce(c)
	data["csp_nonce"] = nonce

	var buf bytes.Buffer
	if err := globalTemplate.ExecuteTemplate(&buf, contentTemplate, data); err != nil {
		c.String(http.StatusInternalServerError, "Ошибка рендеринга: "+err.Error())
		return
	}

	data["ContentHTML"] = template.HTML(buf.String())
	c.HTML(status, "layout.html", data)
}

// RenderError рендерит страницу ошибки с заданным статусом и сообщением.
func RenderError(c *gin.Context, status int, message string) {
	Page(c, status, "errors-404.html", gin.H{"Error": message})
}

// ParseID парсит ID из URL-параметра и возвращает ошибку 400 при неудаче.
// Возвращает ID и bool (успех).
func ParseID(c *gin.Context, paramName string) (uint, bool) {
	idStr := c.Param(paramName)
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		c.HTML(http.StatusBadRequest, "errors-400.html", gin.H{"Error": "Неверный ID"})
		return 0, false
	}
	return uint(id), true
}

// ParseIDQuery парсит ID из query-параметра.
func ParseIDQuery(c *gin.Context, paramName string) (uint, bool) {
	idStr := c.Query(paramName)
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный ID", "code": "bad_request"})
		return 0, false
	}
	return uint(id), true
}
