// internal/pkg/render/helper.go
package render

import (
	"bytes"
	"html/template"
	"net/http"

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
