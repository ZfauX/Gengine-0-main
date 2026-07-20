// Package templatefuncs предоставляет функции-хелперы для использования в шаблонах HTML.
package templatefuncs

import (
	"fmt"
	"html/template"
)

// FuncMap возвращает map с функциями для шаблонов.
func FuncMap() template.FuncMap {
	return template.FuncMap{
		"add1":        add1,
		"sub":         sub,
		"subtract":    subtract,
		"add":         add,
		"loop":        loop,
		"formatBytes": formatBytes,
		"csrfToken":   csrfToken,
		"default":     defaultValue,
		"truncate":    truncate,
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

// subtract возвращает a - b (альяс для sub, для совместимости с шаблонами).
func subtract(a, b int) int {
	return a - b
}

// add возвращает a + b.
func add(a, b int) int {
	return a + b
}

// loop генерирует слайс целых чисел от start до end включительно.
func loop(start, end int) []int {
	s := make([]int, end-start+1)
	for i := range s {
		s[i] = start + i
	}
	return s
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

// csrfToken возвращает строку-заглушку для вставки CSRF-токена в шаблоны.
// В реальном использовании значение подставляется через контекст.
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
