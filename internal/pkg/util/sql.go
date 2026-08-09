// internal/pkg/util/sql.go
// N-4 (pass 38): общие SQL-хелперы — раньше joinPlaceholders/toAnySlice были
// продублированы в game/svc_monitor.go и tournament/service.go.
package util

import "strings"

// JoinPlaceholders возвращает "?, ?, ..." для n значений.
func JoinPlaceholders(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ", ")
}

// ToAnySlice конвертирует срез в []any для GORM/библиотек-аргументов.
func ToAnySlice[T any](s []T) []any {
	result := make([]any, len(s))
	for i, v := range s {
		result[i] = v
	}
	return result
}
