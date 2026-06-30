// internal/pkg/render/helper.go
package render

import (
	"bytes"
	"html/template"
	"net/http"

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

	// Если шаблон не инициализирован (например, в тестах), возвращаем заглушку
	if globalTemplate == nil {
		c.String(http.StatusInternalServerError, "Template engine not initialized")
		return
	}

	// 1. Рендерим контентный шаблон в bytes.Buffer
	var buf bytes.Buffer
	if err := globalTemplate.ExecuteTemplate(&buf, contentTemplate, data); err != nil {
		// В случае ошибки возвращаем текст ошибки, чтобы не падать
		c.String(http.StatusInternalServerError, "Ошибка рендеринга: "+err.Error())
		return
	}

	// 2. Вставляем полученный HTML и отправляем в layout.html
	data["ContentHTML"] = template.HTML(buf.String())
	c.HTML(status, "layout.html", data)
}
