package sanitize

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStripHTML_PlainText(t *testing.T) {
	assert.Equal(t, "hello world", StripHTML("hello world"))
}

func TestStripHTML_RemovesTags(t *testing.T) {
	assert.Equal(t, "", StripHTML("<script>alert('xss')</script>"))
}

func TestStripHTML_WithTagsAndText(t *testing.T) {
	assert.Equal(t, "hello", StripHTML("<b>hello</b>"))
}

func TestStripHTML_Empty(t *testing.T) {
	assert.Equal(t, "", StripHTML(""))
}

func TestStripHTML_XSSAttempts(t *testing.T) {
	assert.Equal(t, "", StripHTML("<img src=x onerror=alert(1)>"))
	assert.Equal(t, "click", StripHTML("<a href='javascript:alert(1)'>click</a>"))
}

func TestStripHTML_NestedTags(t *testing.T) {
	assert.Equal(t, "hello world", StripHTML("<div><p>hello <span>world</span></p></div>"))
}

func TestStripHTML_EncodesAmpersand(t *testing.T) {
	// bluemonday encodes & to &amp; in sanitized output
	assert.Equal(t, "a &amp; b", StripHTML("a &amp; b"))
	assert.Equal(t, "a &amp; b", StripHTML("a & b"))
}

func TestStripHTMLBytes_PlainText(t *testing.T) {
	assert.Equal(t, []byte("hello"), StripHTMLBytes([]byte("hello")))
}

func TestStripHTMLBytes_RemovesTags(t *testing.T) {
	assert.Empty(t, StripHTMLBytes([]byte("<script>evil</script>")))
}

func TestStripHTMLBytes_Nil(t *testing.T) {
	assert.Nil(t, StripHTMLBytes(nil))
}

func TestSanitizeConsistency(t *testing.T) {
	input := "<p>Hello <b>World</b></p>"
	assert.Equal(t, StripHTML(input), string(StripHTMLBytes([]byte(input))))
}
