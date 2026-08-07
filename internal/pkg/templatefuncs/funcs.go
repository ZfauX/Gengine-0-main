// Package templatefuncs предоставляет функции-хелперы для использования в шаблонах HTML.
package templatefuncs

import (
	"fmt"
	"html/template"
	"strings"
	"time"

	"gengine-0/internal/pkg/i18n"
)

// FuncMap возвращает map с функциями для шаблонов.
func FuncMap() template.FuncMap {
	return template.FuncMap{
		"T": func(lang interface{}, key string) string {
			switch v := lang.(type) {
			case string:
				return i18n.Default.T(i18n.Lang(v), key)
			default:
				return i18n.Default.T(i18n.LangRU, key)
			}
		},
		"TF": func(lang interface{}, key string, args ...any) string {
			switch v := lang.(type) {
			case string:
				return i18n.Default.TF(i18n.Lang(v), key, args...)
			default:
				return i18n.Default.TF(i18n.LangRU, key, args...)
			}
		},
		// trName выводит имя элемента (breadcrumb): переводит i18n-ключ (name_key или name с точкой),
		// иначе возвращает сырое имя (например, имя игры/команды) без перевода.
		// Принимает interface{} для устойчивости: отсутствующие ключи map (nil/<no value>)
		// не должны ломать рендер.
		"trName": func(lang interface{}, name, nameKey interface{}) string {
			n, _ := name.(string)
			nk, _ := nameKey.(string)
			key := nk
			if key == "" && strings.Contains(n, ".") {
				key = n
			}
			if key == "" {
				return n
			}
			switch v := lang.(type) {
			case string:
				return i18n.Default.T(i18n.Lang(v), key)
			default:
				return i18n.Default.T(i18n.LangRU, key)
			}
		},
		"add1":           add1,
		"sub":            sub,
		"add":            add,
		"loop":           loop,
		"mod":            mod,
		"formatBytes":    formatBytes,
		"formatDate":     formatDate,
		"formatDateTime": formatDateTime,
		"csrfToken":      csrfToken,
		"default":        defaultValue,
		"truncate":       truncate,
	}
}

// defaultValue возвращает значение по умолчанию, если исходное значение nil.
func defaultValue(defaultVal, val any) any {
	if val != nil {
		return val
	}
	return defaultVal
}

// add1 возвращает i + 1.
func add1(i int) int {
	return i + 1
}

// sub возвращает a - b.
func sub(a, b int) int {
	return a - b
}

// add возвращает a + b.
func add(a, b int) int {
	return a + b
}

// loop генерирует слайс целых чисел от start до end включительно.
func loop(start, end int) []int {
	if end < start {
		return []int{}
	}
	s := make([]int, end-start+1)
	for i := range s {
		s[i] = start + i
	}
	return s
}

// mod возвращает остаток от деления a на b (для шаблонов).
func mod(a, b int) int {
	if b == 0 {
		return 0
	}
	return a % b
}

// formatBytes преобразует байты в человеко-читаемый формат (KB, MB, GB и т.д.).
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// Deprecated: returns literal string "{{ .csrf }}" — use {{.csrf}} directly in templates.
func csrfToken() string {
	return "{{ .csrf }}"
}

// truncate обрезает строку до maxLen символов.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// formatDate форматирует дату локализованно (C9): ru → "02.01.2006",
// en → "02 Jan 2006". Вместо жёсткого "02.01.2006" в шаблонах.
// Принимает interface{} — nil и не-time значения возвращают пустую строку.
func formatDate(lang interface{}, t interface{}) string {
	ts, ok := t.(time.Time)
	if !ok {
		return ""
	}
	loc := "ru"
	if v, ok := lang.(string); ok && v == "en" {
		loc = "en"
	}
	if loc == "en" {
		return ts.Format("02 Jan 2006")
	}
	return ts.Format("02.01.2006")
}

// formatDateTime форматирует дату и время локализованно (C9).
// ru → "02.01.2006 15:04", en → "02 Jan 2006 15:04".
func formatDateTime(lang interface{}, t interface{}) string {
	ts, ok := t.(time.Time)
	if !ok {
		return ""
	}
	loc := "ru"
	if v, ok := lang.(string); ok && v == "en" {
		loc = "en"
	}
	if loc == "en" {
		return ts.Format("02 Jan 2006 15:04")
	}
	return ts.Format("02.01.2006 15:04")
}
