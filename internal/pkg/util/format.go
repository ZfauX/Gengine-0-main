// Package util содержит общие вспомогательные функции.
package util

import (
	"fmt"
	"time"
)

// FormatDuration форматирует длительность в человекочитаемую строку.
func FormatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dч %02dм %02dс", h, m, s)
	}
	return fmt.Sprintf("%dм %02dс", m, s)
}
