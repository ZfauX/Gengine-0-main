// Package sanitize предоставляет функции для очистки HTML от XSS-векторов.
package sanitize

import (
	"github.com/microcosm-cc/bluemonday"
)

var policy = bluemonday.StrictPolicy()

// richPolicy — F-2 (pass 45): политика для WYSIWYG-редакторов описаний
// (игр, уровней, заданий). Разрешает форматирование, таблицы, ссылки и
// медиа-вложения, но блокирует скрипты/обработчики/опасные атрибуты.
// S-H2 (PASS-8): style-атрибут разрешён ТОЛЬКО через AllowStyles с конкретным
// списком безвредных CSS-свойств. Раньше AllowAttrs("style") без AllowStyles()
// пропускал сырой style как есть (bluemonday не санитизирует CSS без
// стилевой политики) — CSS-injection (оверлей/фишинг/трекинг) в доверенном origin.
var richPolicy = bluemonday.UGCPolicy().
	AllowElements("b", "i", "u", "s", "strong", "em", "mark", "sub", "sup",
		"ul", "ol", "li", "blockquote", "pre", "code", "p", "br", "hr", "h2", "h3", "h4",
		"table", "thead", "tbody", "tr", "th", "td", "figure", "figcaption").
	AllowAttrs("style").OnElements("span", "p", "div").
	AllowStyles(
		"color", "background-color", "text-align", "text-decoration", "font-weight",
		"font-style", "font-size", "font-family", "line-height", "margin", "margin-top",
		"margin-bottom", "padding", "padding-top", "padding-bottom", "white-space",
		"width", "height", "border", "border-collapse", "vertical-align",
	).OnElements("span", "p", "div", "table", "th", "td").
	AllowAttrs("colspan", "rowspan").OnElements("th", "td").
	AllowAttrs("src", "alt", "width", "height", "controls", "preload").OnElements("img", "video", "audio", "source").
	AllowAttrs("href", "target", "rel").OnElements("a")

func StripHTML(input string) string {
	return policy.Sanitize(input)
}

func StripHTMLBytes(input []byte) []byte {
	return policy.SanitizeBytes(input)
}

// SanitizeRichText очищает HTML от WYSIWYG-редактора (F-2, pass 45):
// сохраняет форматирование/таблицы/медиа, удаляет скрипты и обработчики.
func SanitizeRichText(input string) string {
	return richPolicy.Sanitize(input)
}
