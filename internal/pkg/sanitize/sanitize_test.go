package sanitize

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStripHTML_PlainText(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "hello world", StripHTML("hello world"))
}

func TestStripHTML_RemovesTags(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", StripHTML("<script>alert('xss')</script>"))
}

func TestStripHTML_WithTagsAndText(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "hello", StripHTML("<b>hello</b>"))
}

func TestStripHTML_Empty(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", StripHTML(""))
}

func TestStripHTML_XSSAttempts(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", StripHTML("<img src=x onerror=alert(1)>"))
	assert.Equal(t, "click", StripHTML("<a href='javascript:alert(1)'>click</a>"))
}

func TestStripHTML_NestedTags(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "hello world", StripHTML("<div><p>hello <span>world</span></p></div>"))
}

func TestStripHTML_EncodesAmpersand(t *testing.T) {
	t.Parallel()
	// bluemonday encodes & to &amp; in sanitized output
	assert.Equal(t, "a &amp; b", StripHTML("a &amp; b"))
	assert.Equal(t, "a &amp; b", StripHTML("a & b"))
}

func TestStripHTMLBytes_PlainText(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []byte("hello"), StripHTMLBytes([]byte("hello")))
}

func TestStripHTMLBytes_RemovesTags(t *testing.T) {
	t.Parallel()
	assert.Empty(t, StripHTMLBytes([]byte("<script>evil</script>")))
}

func TestStripHTMLBytes_Nil(t *testing.T) {
	t.Parallel()
	assert.Nil(t, StripHTMLBytes(nil))
}

func TestSanitizeConsistency(t *testing.T) {
	t.Parallel()
	input := "<p>Hello <b>World</b></p>"
	assert.Equal(t, StripHTML(input), string(StripHTMLBytes([]byte(input))))
}

// F-2 (pass 45): rich-text политика сохраняет форматирование, но удаляет XSS.
func TestSanitizeRichText_KeepsFormatting(t *testing.T) {
	in := `<p style="color:red"><b>Жирный</b> и <i>курсив</i></p><table><tr><td>Ячейка</td></tr></table>`
	out := SanitizeRichText(in)
	assert.Contains(t, out, "<b>Жирный</b>")
	assert.Contains(t, out, "<i>курсив</i>")
	assert.Contains(t, out, "<table>")
	assert.Contains(t, out, "<td>Ячейка</td>")
}

func TestSanitizeRichText_RemovesScripts(t *testing.T) {
	in := `<p onclick="alert(1)">Текст</p><script>alert('xss')</script><a href="javascript:evil()">ссылка</a>`
	out := SanitizeRichText(in)
	assert.NotContains(t, out, "<script")
	assert.NotContains(t, out, "onclick")
	assert.NotContains(t, out, "javascript:")
	assert.Contains(t, out, "Текст")
}

// S-H2 (PASS-8): опасные CSS-свойства в style-атрибуте удаляются (AllowStyles
// с whitelist), а безопасные — сохраняются. Раньше сырой style пропускался.
func TestSanitizeRichText_RemovesDangerousCSS(t *testing.T) {
	in := `<p style="color:red">Безопасно</p><div style="position:fixed;z-index:9999;top:0;left:0;width:100%;height:100%;background:#fff">Оверлей</div><span style="background:url(https://evil.example/track)">Трекинг</span>`
	out := SanitizeRichText(in)
	// Безопасное свойство сохраняется (bluemonday нормализует пробел после `:`).
	assert.Contains(t, out, "color: red")
	// Опасные свойства удаляются (позиционирование/фон-URL).
	assert.NotContains(t, out, "position:fixed")
	assert.NotContains(t, out, "z-index")
	assert.NotContains(t, out, "url(")
}
