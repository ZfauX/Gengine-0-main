// Package sanitize предоставляет функции для очистки HTML от XSS-векторов.
package sanitize

import (
	"github.com/microcosm-cc/bluemonday"
)

var policy = bluemonday.StrictPolicy()

func StripHTML(input string) string {
	return policy.Sanitize(input)
}

func StripHTMLBytes(input []byte) []byte {
	return policy.SanitizeBytes(input)
}
