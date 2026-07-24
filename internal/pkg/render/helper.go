// internal/pkg/render/helper.go
package render

import (
	"bytes"
	"html/template"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"

	"gengine-0/internal/pkg/middleware"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// BreadcrumbItem представляет один элемент навигационной цепочки
type BreadcrumbItem struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

var (
	mu                 sync.RWMutex
	globalTemplate     *template.Template
	templateDevPattern string
	templateFuncMap    template.FuncMap
)

// SetTemplate сохраняет общий *template.Template для использования в хелпере.
func SetTemplate(t *template.Template) {
	mu.Lock()
	globalTemplate = t
	mu.Unlock()
}

// EnableDevMode включает горячую перезагрузку шаблонов для режима разработки.
// При каждом вызове Page() шаблоны будут перечитываться с диска.
func EnableDevMode(baseDir string, funcMap template.FuncMap) {
	mu.Lock()
	templateDevPattern = filepath.Join(baseDir, "internal", "domain", "*", "templates", "*.html")
	templateFuncMap = funcMap
	mu.Unlock()
}

// Page рендерит указанный подшаблон в буфер, вставляет результат как ContentHTML в layout.html.
// contentTemplate — имя шаблона (например "auth-login.html"), которое должно быть определено в общем наборе.
func Page(c *gin.Context, status int, contentTemplate string, data gin.H) {
	if data == nil {
		data = gin.H{}
	}

	var tmpl *template.Template

	if templateDevPattern != "" {
		mu.Lock()
		t := template.New("")
		t.Funcs(templateFuncMap)
		if _, err := t.ParseGlob(templateDevPattern); err != nil {
			log.Error().Err(err).Msg("Render: hot-reload template parse error")
			tmpl = globalTemplate
		} else {
			globalTemplate = t
			tmpl = t
		}
		mu.Unlock()
	} else {
		mu.RLock()
		tmpl = globalTemplate
		mu.RUnlock()
	}

	if tmpl == nil {
		c.String(http.StatusInternalServerError, "Template engine not initialized")
		return
	}

	// Добавляем nonce в данные шаблона
	nonce := middleware.GetCSPNonce(c)
	data["csp_nonce"] = nonce

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, contentTemplate, data); err != nil {
		log.Error().Err(err).Msg("Render: template execution error")
		c.String(http.StatusInternalServerError, "Внутренняя ошибка сервера")
		return
	}

	data["ContentHTML"] = template.HTML(buf.String())
	c.HTML(status, "layout.html", data)
}

// RenderError рендерит страницу ошибки с заданным статусом и сообщением.
// Автоматически выбирает шаблон по статусу (400, 403, 404, 500).
func RenderError(c *gin.Context, status int, message string) {
	if message == "" {
		message = defaultErrorMessage(status)
	}
	templateName := errorTemplateForStatus(status)
	Page(c, status, templateName, gin.H{"Error": message})
}

// RenderErrorPage рендерит страницу ошибки без сообщения (используется для 403/500).
func RenderErrorPage(c *gin.Context, status int) {
	templateName := errorTemplateForStatus(status)
	Page(c, status, templateName, gin.H{})
}

// defaultErrorMessage возвращает стандартное сообщение для HTTP-статуса.
func defaultErrorMessage(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "Неверный запрос"
	case http.StatusForbidden:
		return "Доступ запрещён"
	case http.StatusNotFound:
		return "Не найдено"
	case http.StatusInternalServerError:
		return "Внутренняя ошибка сервера"
	default:
		return "Ошибка"
	}
}

// errorTemplateForStatus возвращает имя шаблона для статуса ошибки.
func errorTemplateForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "errors-400.html"
	case http.StatusForbidden:
		return "errors-403.html"
	case http.StatusNotFound:
		return "errors-404.html"
	case http.StatusTooManyRequests:
		return "errors-429.html"
	case http.StatusInternalServerError:
		return "errors-500.html"
	default:
		return "errors-500.html"
	}
}

// ParseID парсит ID из URL-параметра и возвращает ошибку 400 при неудаче.
// Возвращает ID и bool (успех).
func ParseID(c *gin.Context, paramName string) (uint, bool) {
	idStr := c.Param(paramName)
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		Page(c, http.StatusBadRequest, "errors-400.html", gin.H{"Error": "Неверный ID"})
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

// SetBreadcrumb добавляет breadcrumb в данные шаблона.
// data — карта gin.H, items — список элементов навигации.
func SetBreadcrumb(data gin.H, items ...BreadcrumbItem) {
	if data == nil {
		data = gin.H{}
	}
	data["Breadcrumb"] = items
}

// SetFlash сохраняет flash-сообщение в сессии.
func SetFlash(c *gin.Context, key, value string) {
	session := sessions.Default(c)
	session.Set(key, value)
	if err := session.Save(); err != nil {
		log.Error().Err(err).Str("key", key).Msg("SetFlash: failed to save session")
	}
}

// GetFlash читает и удаляет flash-сообщение из сессии.
func GetFlash(c *gin.Context, key string) string {
	session := sessions.Default(c)
	val, ok := session.Get(key).(string)
	if !ok {
		return ""
	}
	session.Delete(key)
	if err := session.Save(); err != nil {
		log.Error().Err(err).Str("key", key).Msg("GetFlash: failed to save session")
	}
	return val
}
