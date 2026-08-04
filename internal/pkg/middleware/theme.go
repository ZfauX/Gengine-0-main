// internal/pkg/middleware/theme.go
package middleware

import (
	"context"
	"strings"

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
	ts := themeSettingsLoader(c.Request.Context(), userID)
	if ts != nil {
		c.Set("theme_settings", ts)
	}
}

// ThemeSettingsFromCtx возвращает настройки темы из контекста (если есть).
func ThemeSettingsFromCtx(c *gin.Context) (any, bool) {
	v, ok := c.Get("theme_settings")
	return v, ok
}
