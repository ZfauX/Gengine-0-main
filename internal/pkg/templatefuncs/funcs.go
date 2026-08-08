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
		"add1":                add1,
		"sub":                 sub,
		"add":                 add,
		"loop":                loop,
		"mod":                 mod,
		"formatBytes":         formatBytes,
		"formatDate":          formatDate,
		"formatDateTime":      formatDateTime,
		"formatDateTimeInput": formatDateTimeInput,
		"formatTime":          formatTime,
		"initials":            initials,
		"csrfToken":           csrfToken,
		"default":             defaultValue,
		"truncate":            truncate,
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

// initials возвращает первую руну строки в верхнем регистре (HIGH-2).
// Go-шаблонный `slice` режет строку по байтам — для кириллицы это даёт
// битый символ. Здесь работаем по рунам.
func initials(s interface{}) string {
	str, ok := s.(string)
	if !ok || str == "" {
		return "?"
	}
	runes := []rune(str)
	return strings.ToUpper(string(runes[0]))
}

// formatDate форматирует дату локализованно (C9): ru → "02.01.2006",
// en → "02 Jan 2006". Вместо жёсткого "02.01.2006" в шаблонах.
// Принимает time.Time или *time.Time (H1, pass 30 — поля модели часто
// указатели); nil и не-time значения возвращают пустую строку.
// M5 (pass 30): второй аргумент — смещение пользователя в минутах от UTC
// (число из cookie tz_offset); 0/отсутствие — UTC. Форматирование
// выполняется в локальном времени пользователя.
func formatDate(lang interface{}, tzOffset interface{}, t interface{}) string {
	ts, ok := asTime(t)
	if !ok {
		return ""
	}
	ts = applyTZOffset(ts, tzOffset)
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
func formatDateTime(lang interface{}, tzOffset interface{}, t interface{}) string {
	ts, ok := asTime(t)
	if !ok {
		return ""
	}
	ts = applyTZOffset(ts, tzOffset)
	loc := "ru"
	if v, ok := lang.(string); ok && v == "en" {
		loc = "en"
	}
	if loc == "en" {
		return ts.Format("02 Jan 2006 15:04")
	}
	return ts.Format("02.01.2006 15:04")
}

// formatDateTimeInput форматирует время в формате datetime-local
// (2006-01-02T15:04) с учётом TZOffset пользователя (UX-1, pass 31).
// Используется в value="..." полей datetime-local, чтобы редактирование
// показывало локальное время, а не UTC.
func formatDateTimeInput(tzOffset interface{}, t interface{}) string {
	ts, ok := asTime(t)
	if !ok {
		return ""
	}
	ts = applyTZOffset(ts, tzOffset)
	return ts.Format("2006-01-02T15:04")
}

// formatTime форматирует время "15:04:05" с учётом TZOffset пользователя
// (UX-3, pass 31) — для логов и попыток, где дата не нужна.
func formatTime(tzOffset interface{}, t interface{}) string {
	ts, ok := asTime(t)
	if !ok {
		return ""
	}
	ts = applyTZOffset(ts, tzOffset)
	return ts.Format("15:04:05")
}

// applyTZOffset сдвигает время на смещение пользователя в минутах от UTC (M5).
// Принимает int/float64/string; нечисловое значение игнорируется (UTC).
func applyTZOffset(t time.Time, tzOffset interface{}) time.Time {
	var minutes int
	switch v := tzOffset.(type) {
	case int:
		minutes = v
	case int64:
		minutes = int(v)
	case float64:
		minutes = int(v)
	default:
		return t
	}
	if minutes == 0 {
		return t
	}
	return t.Add(time.Duration(minutes) * time.Minute)
}

// asTime принимает time.Time или *time.Time и возвращает разыменованное
// значение. nil-указатель и не-time типы дают ok=false.
func asTime(v interface{}) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case *time.Time:
		if t == nil {
			return time.Time{}, false
		}
		return *t, true
	default:
		return time.Time{}, false
	}
}
