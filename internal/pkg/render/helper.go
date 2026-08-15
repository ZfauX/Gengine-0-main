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

// GetTemplate возвращает текущий общий шаблон (nil, если не установлен).
// Используется тестами для восстановления глобального шаблона после
// переопределения (D6) — исключает interference между тест-пакетами.
func GetTemplate() *template.Template {
	mu.RLock()
	defer mu.RUnlock()
	return globalTemplate
}

// SetTemplateForTest временно устанавливает шаблон и возвращает cleanup,
// который восстанавливает предыдущий. Удобно в handler-тестах: не нужно
// вручную помнить/восстанавливать глобальное состояние.
func SetTemplateForTest(t *template.Template) func() {
	prev := GetTemplate()
	SetTemplate(t)
	return func() { SetTemplate(prev) }
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
	defer func() { _ = watcher.Close() }()

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

// gengineSessionCookie — имя cookie сессии (должно совпадать с router.go:
// sessions.Sessions("gengine_session", store)).
const gengineSessionCookie = "gengine_session"

// bufferPool (P-1, PASS-9): sync.Pool буферов для двухпроходного рендера.
// Раньше каждый HTML-запрос аллоцировал bytes.Buffer (~50-100KB) — топ-аллокатор
// по pprof (bytes.growSlice 17.4%, ShowLoginForm 15.26% cum).
var bufferPool = sync.Pool{
	New: func() any { return new(bytes.Buffer) },
}

// maxPooledBufferSize (L13, PASS-17): буферы больше 1MB в пул не возвращаем —
// одна очень большая страница иначе навсегда пиннит огромный буфер в sync.Pool.
const maxPooledBufferSize = 1 << 20

// putBuffer возвращает буфер в пул, если он не разросся сверх лимита.
func putBuffer(buf *bytes.Buffer) {
	if buf != nil && buf.Cap() <= maxPooledBufferSize {
		bufferPool.Put(buf)
	}
}

// hasSessionCookie — дешёвая проверка наличия cookie сессии до sessions.Default:
// для анонимных запросов (большинство публичных GET) сессия гарантированно пустая,
// открывать её (3-5 аллокаций Registry/Values) не нужно.
func hasSessionCookie(c *gin.Context) bool {
	// L3 (PASS-10): точный парсинг cookie — strings.Contains("gengine_session=")
	// ложно совпадал с `gengine_session_x=...`.
	_, err := c.Request.Cookie(gengineSessionCookie)
	return err == nil
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

	// M5 (pass 30): смещение пользователя в минутах от UTC из cookie tz_offset
	// (устанавливается JS). Используется formatDate/formatDateTime.
	data["TZOffset"] = tzOffsetFromCookie(c)

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
	// P7 (PASS-6): сессия открывается ОДИН раз — раньше каждый GetFlash делал
	// sessions.Default(c) (парсинг cookie на каждый ключ).
	// P-1 (PASS-9): без cookie сессии сессия не открывается вовсе (анонимные GET).
	if hasSessionCookie(c) {
		session := sessions.Default(c)
		for _, key := range []string{"error", "success", "flash", "gameplay_error", "gameplay_hint"} {
			if flash := getFlashFromSession(session, key); flash != "" {
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
	}

	// Add canonical URL if not set (SEO-2, pass 39): раньше был всегда пустым —
	// <link rel="canonical" href=""> на каждой странице. Заполняем absolute
	// self-URL с учётом reverse-proxy (как og:image / HSTS).
	if _, exists := data["CanonicalURL"]; !exists {
		canonical := ""
		if c.Request.URL != nil && c.Request.URL.Path != "" {
			scheme := "http"
			if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
				scheme = "https"
			}
			canonical = scheme + "://" + c.Request.Host + c.Request.URL.Path
		}
		data["CanonicalURL"] = canonical
	}

	// P-3 (PASS-13): HTML-кэш анонимных публичных GET.
	// H1 (PASS-15): проверяем кэш ДО рендера content-шаблона — раньше
	// tryServeAnonCache вызывался после ExecuteTemplate, и на кэш-хите
	// самый дорогой шаг (content-template walk) исполнялся впустую.
	// isCacheableAnon и ключ не зависят от контента — перестановка безопасна.
	if tryServeAnonCache(c, status, data) {
		return
	}

	// P-1 (PASS-9): буфер из sync.Pool — раньше `var buf bytes.Buffer` на каждый запрос.
	//nolint:errcheck // sync.Pool.Get возвращает any, ошибки нет (ложное срабатывание)
	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer putBuffer(buf)

	if err := tmpl.ExecuteTemplate(buf, contentTemplate, data); err != nil {
		log.Error().Err(err).Msg("Render: template execution error")
		c.String(http.StatusInternalServerError, i18n.T("generic.server_error"))
		return
	}

	data["ContentHTML"] = template.HTML(buf.String())

	// M4 (pass 30): если контент-шаблон определяет блок "ExtraHead"
	// (например, OG-теги на games-show), рендерим его в layout.
	// perf F3 (PASS-10): headBuf из sync.Pool — раньше свежий буфер на каждую
	// страницу с ExtraHead (вторая копия строки + аллокация).
	if tmpl.Lookup("ExtraHead") != nil {
		//nolint:errcheck // sync.Pool.Get возвращает any, ошибки нет (ложное срабатывание)
		headBuf := bufferPool.Get().(*bytes.Buffer)
		headBuf.Reset()
		defer putBuffer(headBuf)
		if err := tmpl.ExecuteTemplate(headBuf, "ExtraHead", data); err != nil {
			log.Error().Err(err).Msg("Render: ExtraHead template execution error")
		} else {
			data["ExtraHead"] = template.HTML(headBuf.String())
		}
	}

	// Рендерим layout в буфер: при кэшировании нужен сам HTML (не gin-рендер),
	// иначе невозможно сохранить его в кэш. Для не-кэшируемых страниц это тоже
	// безопасно (c.Data вместо c.HTML — тот же HTML, тот же Content-Type).
	//nolint:errcheck // sync.Pool.Get возвращает any, ошибки нет (ложное срабатывание)
	layoutBuf := bufferPool.Get().(*bytes.Buffer)
	layoutBuf.Reset()
	defer putBuffer(layoutBuf)
	if err := tmpl.ExecuteTemplate(layoutBuf, "layout.html", data); err != nil {
		log.Error().Err(err).Msg("Render: layout template execution error")
		c.String(http.StatusInternalServerError, i18n.T("generic.server_error"))
		return
	}
	// Сохраняем в кэш анонимных страниц (если применимо).
	storeAnonCache(c, data, layoutBuf.Bytes())
	c.Data(status, "text/html; charset=utf-8", layoutBuf.Bytes())
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

// tzOffsetFromCookie читает смещение пользователя в минутах от UTC из cookie
// tz_offset (устанавливается JS через getTimezoneOffset()). Возвращает 0,
// если cookie отсутствует/нечисловой — UTC по умолчанию (M5, pass 30).
func tzOffsetFromCookie(c *gin.Context) int {
	raw, err := c.Cookie("tz_offset")
	if err != nil || raw == "" {
		return 0
	}
	// Диапазон реальных смещений — [-840, 840] минут; грубые значения игнорируем.
	offset, err := strconv.Atoi(raw)
	if err != nil || offset < -840 || offset > 840 {
		return 0
	}
	return offset
}

// TZOffsetFromCookie — экспортируемая версия tzOffsetFromCookie для
// хендлеров, которым нужно применять смещение при парсинге форм (UX-1, pass 31).
func TZOffsetFromCookie(c *gin.Context) int {
	return tzOffsetFromCookie(c)
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
	return getFlashFromSession(session, key)
}

// getFlashFromSession (P7, PASS-6): читает и удаляет flash из УЖЕ открытой
// сессии — для циклов по нескольким ключам (render.Page) не парсим cookie
// на каждый ключ.
func getFlashFromSession(session sessions.Session, key string) string {
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
