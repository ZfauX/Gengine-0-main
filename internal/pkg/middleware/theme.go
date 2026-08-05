// internal/pkg/middleware/theme.go
package middleware

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// ThemeSettingsLoader возвращает настройки темы для пользователя.
// Настраивается один раз при старте приложения через SetThemeSettingsLoader.
type ThemeSettingsLoader func(ctx context.Context, userID uint) any

var themeSettingsLoader ThemeSettingsLoader

// SetThemeSettingsLoader регистрирует функцию загрузки настроек темы.
// Вызывается из app-слоя при инициализации.
func SetThemeSettingsLoader(fn ThemeSettingsLoader) {
	themeSettingsLoader = fn
}

// --- Короткий TTL-кэш настроек темы (P5) ---
// Настройки темы редкоМеняются, но читаются на каждый авторизованный HTML-запрос.
// Кэш 60с убирает лишний DB-запрос, не давая долго жить устаревшим данным.

const themeCacheTTL = 60 * time.Second

type themeCacheEntry struct {
	value   any
	expires time.Time
}

var (
	themeCacheMu  sync.Mutex
	themeCache    = make(map[uint]themeCacheEntry)
	themeCacheOnc sync.Once
)

func cachedThemeSettings(userID uint) (any, bool) {
	themeCacheMu.Lock()
	defer themeCacheMu.Unlock()
	e, ok := themeCache[userID]
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expires) {
		delete(themeCache, userID)
		return nil, false
	}
	return e.value, true
}

func cacheThemeSettings(userID uint, value any) {
	themeCacheMu.Lock()
	themeCache[userID] = themeCacheEntry{value: value, expires: time.Now().Add(themeCacheTTL)}
	themeCacheMu.Unlock()
}

// InvalidateThemeCache сбрасывает кэш темы пользователя после сохранения настроек.
func InvalidateThemeCache(userID uint) {
	themeCacheMu.Lock()
	delete(themeCache, userID)
	themeCacheMu.Unlock()
}

// themeCacheCleanup периодически вычищает истёкшие записи (предотвращает рост памяти).
func themeCacheCleanup() {
	themeCacheOnc.Do(func() {
		go func() {
			for {
				time.Sleep(5 * time.Minute)
				now := time.Now()
				themeCacheMu.Lock()
				for uid, e := range themeCache {
					if now.After(e.expires) {
						delete(themeCache, uid)
					}
				}
				themeCacheMu.Unlock()
			}
		}()
	})
}

// loadThemeSettings загружает настройки темы текущего пользователя в контекст
// под ключом "theme_settings". Вызывается из auth-мидлварей после установки userID.
// Для анонимных пользователей (userID == 0) настройки не загружаются — layout
// использует значения по умолчанию.
// Не загружаем для /api, /ws, /static, /uploads — там не рендерится HTML и
// настройки темы не нужны (убирает лишний DB-запрос на каждый запрос, C1).
func loadThemeSettings(c *gin.Context) {
	if themeSettingsLoader == nil {
		return
	}
	path := c.Request.URL.Path
	if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/ws/") ||
		strings.HasPrefix(path, "/static/") || strings.HasPrefix(path, "/uploads/") {
		return
	}
	userID := c.GetUint("userID")
	if userID == 0 {
		return
	}
	themeCacheCleanup()

	if cached, ok := cachedThemeSettings(userID); ok {
		c.Set("theme_settings", cached)
		return
	}
	ts := themeSettingsLoader(c.Request.Context(), userID)
	if ts != nil {
		cacheThemeSettings(userID, ts)
		c.Set("theme_settings", ts)
	}
}

// ThemeSettingsFromCtx возвращает настройки темы из контекста (если есть).
func ThemeSettingsFromCtx(c *gin.Context) (any, bool) {
	v, ok := c.Get("theme_settings")
	return v, ok
}
