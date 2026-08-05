// internal/pkg/render/helper.go
package render

import (
	"bytes"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"gengine-0/internal/pkg/i18n"
	"gengine-0/internal/pkg/middleware"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/fsnotify/fsnotify"
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
	staticVersion      string
)

// SetStaticVersion задаёт версию статики для ?v= в шаблонах (UX5 — единый
// источник вместо ручной синхронизации ?v= в layout и precache в sw.js).
func SetStaticVersion(v string) {
	staticVersion = v
}

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
	// Initial load
	t := template.New("")
	t.Funcs(templateFuncMap)
	if _, err := t.ParseGlob(templateDevPattern); err == nil {
		globalTemplate = t
	}
	// Start file watcher
	go watchTemplates(baseDir, templateDevPattern, templateFuncMap)
	mu.Unlock()
}

func watchTemplates(baseDir, pattern string, funcMap template.FuncMap) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Warn().Err(err).Msg("Render: fsnotify disabled")
		return
	}
	defer watcher.Close()

	// Watch all template directories
	for _, dir := range []string{
		"internal/domain/game/templates",
		"internal/domain/team/templates",
		"internal/domain/tournament/templates",
		"internal/domain/user/templates",
		"internal/domain/level/templates",
		"internal/domain/monitor/templates",
		"internal/domain/admin/templates",
		"internal/domain/export/templates",
		"internal/domain/social/templates",
		"internal/domain/notification/templates",
		"internal/domain/calendar/templates",
	} {
		fullPath := filepath.Join(baseDir, dir)
		if stat, err := os.Stat(fullPath); err == nil && stat.IsDir() {
			if addErr := watcher.Add(fullPath); addErr != nil {
				log.Warn().Err(addErr).Str("dir", fullPath).Msg("Render: failed to watch template dir")
			}
		}
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) != 0 {
				mu.Lock()
				t := template.New("")
				t.Funcs(funcMap)
				if _, err := t.ParseGlob(pattern); err != nil {
					log.Error().Err(err).Msg("Render: hot-reload template parse error")
				} else {
					globalTemplate = t
					log.Debug().Str("file", event.Name).Msg("Render: templates reloaded")
				}
				mu.Unlock()
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Warn().Err(err).Msg("Render: fsnotify error")
		}
	}
}

// Page рендерит указанный подшаблон в буфер, вставляет результат как ContentHTML в layout.html.
// contentTemplate — имя шаблона (например "auth-login.html"), которое должно быть определено в общем наборе.
func Page(c *gin.Context, status int, contentTemplate string, data gin.H) {
	if data == nil {
		data = gin.H{}
	}

	mu.RLock()
	tmpl := globalTemplate
	mu.RUnlock()

	if tmpl == nil {
		c.String(http.StatusInternalServerError, "Template engine not initialized")
		return
	}

	// Добавляем язык в данные шаблона для {{ T }}/{{ TF }} в шаблонах
	lang := i18n.FromCtx(c)
	data["Lang"] = string(lang)

	// Версия статики для ?v= (UX5). Если не задана — пустая строка (нет суффикса).
	data["StaticVersion"] = staticVersion

	// Добавляем информацию о пользователе из контекста (если не переопределено хендлером)
	if _, exists := data["CurrentUserID"]; !exists {
		data["CurrentUserID"] = c.GetUint("userID")
	}
	if _, exists := data["IsAdmin"]; !exists {
		data["IsAdmin"] = middleware.IsAdmin(c)
	}

	// Добавляем настройки темы из контекста (загружены auth-мидлварью для авторизованных)
	if _, exists := data["ThemeSettings"]; !exists {
		if ts, ok := middleware.ThemeSettingsFromCtx(c); ok {
			data["ThemeSettings"] = ts
		}
	}

	// Добавляем nonce в данные шаблона
	nonce := middleware.GetCSPNonce(c)
	data["csp_nonce"] = nonce

	// Add flash message from session — поддерживаем разные ключи (error/success/flash/...)
	// Хендлеры ставят SetFlash(c, "error"|"success"|"gameplay_error"|"gameplay_hint", msg).
	for _, key := range []string{"error", "success", "flash", "gameplay_error", "gameplay_hint"} {
		if flash := GetFlash(c, key); flash != "" {
			data["Flash"] = flash
			switch key {
			case "error", "gameplay_error":
				data["FlashType"] = "error"
			case "gameplay_hint":
				data["FlashType"] = "info"
			default:
				data["FlashType"] = "success"
			}
			break
		}
	}

	// Add canonical URL if not set
	if _, exists := data["CanonicalURL"]; !exists {
		data["CanonicalURL"] = ""
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, contentTemplate, data); err != nil {
		log.Error().Err(err).Msg("Render: template execution error")
		c.String(http.StatusInternalServerError, i18n.T("generic.server_error"))
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
		return i18n.T("generic.bad_request")
	case http.StatusForbidden:
		return i18n.T("generic.forbidden")
	case http.StatusNotFound:
		return i18n.T("generic.not_found")
	case http.StatusInternalServerError:
		return i18n.T("generic.server_error")
	default:
		return i18n.T("generic.error")
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
	case http.StatusServiceUnavailable:
		return "errors-503.html"
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
		Page(c, http.StatusBadRequest, "errors-400.html", gin.H{"Error": i18n.T("generic.invalid_id")})
		return 0, false
	}
	return uint(id), true
}

// Tr переводит ключ i18n в язык текущего запроса (для использования в хендлерах).
func Tr(c *gin.Context, key string) string {
	if i18n.Default == nil {
		return key
	}
	return i18n.Default.T(i18n.FromCtx(c), key)
}

// Trf переводит ключ i18n с аргументами форматирования в язык текущего запроса.
func Trf(c *gin.Context, key string, args ...any) string {
	if i18n.Default == nil {
		return key
	}
	return i18n.Default.TF(i18n.FromCtx(c), key, args...)
}

// ParseIDQuery парсит ID из query-параметра.
func ParseIDQuery(c *gin.Context, paramName string) (uint, bool) {
	idStr := c.Query(paramName)
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": i18n.T("generic.invalid_id"), "code": "bad_request"})
		return 0, false
	}
	return uint(id), true
}

// SetBreadcrumb добавляет breadcrumb в данные шаблона.
// data — карта gin.H, items — список элементов навигации.
// Если Name содержит точку (i18n-ключ вида "nav.home") — выводится через name_key (T),
// иначе — как raw-имя (например, имя игры/команды) через name без перевода.
func SetBreadcrumb(data gin.H, items ...BreadcrumbItem) {
	if data == nil {
		data = gin.H{}
	}
	// Конвертируем в формат, понятный шаблону (слайс map'ов)
	breadcrumbs := make([]map[string]string, len(items))
	for i, item := range items {
		crumb := map[string]string{
			"url": item.URL,
		}
		if strings.Contains(item.Name, ".") {
			crumb["name_key"] = item.Name
			crumb["name"] = item.Name
		} else {
			crumb["name"] = item.Name
		}
		breadcrumbs[i] = crumb
	}
	data["Breadcrumbs"] = breadcrumbs
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
