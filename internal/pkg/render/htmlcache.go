// internal/pkg/render/htmlcache.go
//
// HTML-кэш анонимных публичных страниц (P-3, PASS-13).
//
// Проблема: template-рендер — 37% CPU под нагрузкой (pprof PASS-13). Для
// анонимных публичных GET (/ и /games) результат стабилен на коротком окне —
// можно отдавать готовый HTML без повторного рендера.
//
// Безопасность (почему предыдущий отказ P-2 в PASS-10 снят): страницы содержат
// per-request nonce (CSP) и CSRF-токен. Кэшируется HTML С ПЛЕЙСХОЛДЕРАМИ, а при
// отдаче подставляются СВЕЖИЕ nonce/CSRF текущего запроса. Каждый клиент
// получает уникальные значения — CSP/CSRF не ослабляются.
//
// Кэшируются только страницы из allowlist, только GET, только анонимные
// (без cookie сессии), только 200. Ключ: path + query + lang.

package render

import (
	"bytes"
	"net/http"
	"strconv"
	"sync"
	"time"

	"gengine-0/internal/pkg/i18n"
	"gengine-0/internal/pkg/middleware"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// noncePlaceholder / csrfPlaceholder — плейсхолдеры в кэшированном HTML.
// При отдаче заменяются на свежие значения запроса. Выбраны так, чтобы не
// встречаться в реальном контенте.
const (
	noncePlaceholder = "GENGINE_CSP_NONCE_PLACEHOLDER_7f3a"
	csrfPlaceholder  = "GENGINE_CSRF_TOKEN_PLACEHOLDER_9b2c"

	// htmlCacheTTL — окно кэша анонимных страниц. 30с: рендер уходит из 99%
	// запросов, но данные (новые игры и т.п.) появляются без заметной задержки.
	htmlCacheTTL = 30 * time.Second

	// htmlCacheMaxEntries — верхняя граница кэша (lazy sweep).
	htmlCacheMaxEntries = 256
)

// htmlCacheEntry — запись кэша.
type htmlCacheEntry struct {
	body    []byte
	expires time.Time
}

// htmlCache — in-memory кэш готового HTML анонимных страниц.
type htmlCache struct {
	mu    sync.Mutex
	items map[string]htmlCacheEntry
}

var anonHTMLCache = &htmlCache{items: make(map[string]htmlCacheEntry)}

// cachedAnonPaths — allowlist кэшируемых анонимных путей (GET, публичные).
var cachedAnonPaths = map[string]bool{
	"/":      true,
	"/games": true,
}

// anonCacheKey строит ключ кэша: path + "?" + query + lang + tzOffset.
// M1 (PASS-15): TZOffset входит в ключ — иначе даты рендерятся в TZ первого
// анонима, чей запрос попал в кэш, и второй аноним с другим tz видит чужие.
func anonCacheKey(c *gin.Context) string {
	lang := string(i18n.FromCtx(c))
	tz := tzOffsetFromCookie(c)
	q := c.Request.URL.RawQuery
	if q == "" {
		return c.Request.URL.Path + "|" + lang + "|tz" + strconv.Itoa(tz)
	}
	return c.Request.URL.Path + "?" + q + "|" + lang + "|tz" + strconv.Itoa(tz)
}

// isCacheableAnon определяет, можно ли кэшировать запрос: GET, статус 200,
// аноним (нет cookie сессии), путь в allowlist.
func isCacheableAnon(c *gin.Context) bool {
	if c.Request.Method != http.MethodGet {
		return false
	}
	if !cachedAnonPaths[c.Request.URL.Path] {
		return false
	}
	// Аноним = нет session cookie (авторизованные имеют и JWT, и сессию;
	// для анонимов сессия не создаётся до записи). Если cookie сессии есть —
	// не кэшируем (страница может содержать персональные данные).
	if hasSessionCookie(c) {
		return false
	}
	// Не кэшируем, если хендлер уже поставил flash/персональные данные.
	if c.GetUint("userID") != 0 {
		return false
	}
	return true
}

// tryServeAnonCache пробует отдать кэшированную версию страницы. Возвращает
// true, если запрос полностью обработан (кэш-хит). Перед вызовом data должен
// быть полностью подготовлен (lang, nonce, csrf) — ключ и allowlist проверяются
// внутри, чтобы не ломать поток Page.
func tryServeAnonCache(c *gin.Context, status int, data gin.H) bool {
	if status != http.StatusOK || !isCacheableAnon(c) {
		return false
	}
	key := anonCacheKey(c)
	now := time.Now()

	anonHTMLCache.mu.Lock()
	e, ok := anonHTMLCache.items[key]
	if ok && now.Before(e.expires) {
		body := e.body
		anonHTMLCache.mu.Unlock()
		// H2 (PASS-15): подстановка nonce/CSRF через bytes.ReplaceAll — без
		// string-конверсий и полных копий HTML (раньше string→2×Replace→[]byte).
		out := body
		nonce := middleware.GetCSPNonce(c)
		if nonce != "" {
			out = bytes.ReplaceAll(out, []byte(noncePlaceholder), []byte(nonce))
		}
		if csrfStr, ok := data["csrf"].(string); ok && csrfStr != "" {
			out = bytes.ReplaceAll(out, []byte(csrfPlaceholder), []byte(csrfStr))
		}
		c.Data(status, "text/html; charset=utf-8", out)
		return true
	}
	// Промах — снимаем lock и даём Page отрендерить; результат запишется
	// через storeAnonCache (вызывается из Page после рендера).
	anonHTMLCache.mu.Unlock()
	return false
}

// storeAnonCache сохраняет отрендеренный HTML в кэш (если страница кэшируемая).
// Должен вызываться из Page ПОСЛЕ полного рендера layout в buf.
func storeAnonCache(c *gin.Context, data gin.H, rendered []byte) {
	if !isCacheableAnon(c) {
		return
	}
	// H2 (PASS-15): заменяем реальные nonce/CSRF на плейсхолдеры через
	// bytes.ReplaceAll — без string-конверсий и копий.
	out := rendered
	nonce := middleware.GetCSPNonce(c)
	if nonce != "" {
		out = bytes.ReplaceAll(out, []byte(nonce), []byte(noncePlaceholder))
	}
	if csrfStr, ok := data["csrf"].(string); ok && csrfStr != "" {
		out = bytes.ReplaceAll(out, []byte(csrfStr), []byte(csrfPlaceholder))
	}

	key := anonCacheKey(c)
	now := time.Now()
	anonHTMLCache.mu.Lock()
	defer anonHTMLCache.mu.Unlock()
	// Lazy sweep.
	if len(anonHTMLCache.items) > htmlCacheMaxEntries {
		for k, v := range anonHTMLCache.items {
			if !now.Before(v.expires) {
				delete(anonHTMLCache.items, k)
			}
		}
	}
	anonHTMLCache.items[key] = htmlCacheEntry{body: out, expires: now.Add(htmlCacheTTL)}
}

// clearAnonCache очищает кэш (для тестов).
func clearAnonCache() {
	anonHTMLCache.mu.Lock()
	anonHTMLCache.items = make(map[string]htmlCacheEntry)
	anonHTMLCache.mu.Unlock()
}

// Линтер: log и bytes используются в этом пакете (helper.go), здесь — для
// явной привязки импортов, если будущие правки их уберут.
var _ = log.Debug
var _ = bytes.NewBuffer
