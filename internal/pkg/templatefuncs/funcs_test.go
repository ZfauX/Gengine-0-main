package templatefuncs

import (
	"html/template"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFuncMap_ContainsExpectedKeys(t *testing.T) {
	fm := FuncMap()
	expected := []string{"add1", "sub", "add", "loop", "formatBytes", "csrfToken", "default", "truncate", "T", "TF"}
	for _, key := range expected {
		assert.Contains(t, fm, key)
	}
}

func TestT_TakesLang(t *testing.T) {
	fm := FuncMap()
	tFn, ok := fm["T"].(func(interface{}, string) string)
	assert.True(t, ok, "T should be func(lang interface{}, key string) string")
	assert.Equal(t, "Game not found", tFn("en", "game.not_found"))
	assert.Equal(t, "nonexistent", tFn("en", "nonexistent"))
}

func TestTF_TakesLang(t *testing.T) {
	fm := FuncMap()
	tfFn, ok := fm["TF"].(func(interface{}, string, ...any) string)
	assert.True(t, ok, "TF should be func(lang interface{}, key string, args ...any) string")
	assert.Equal(t, "A level with position 5 already exists in this game", tfFn("en", "level.position_exists", 5))
}

func TestFuncMap_IsTemplateFuncMap(t *testing.T) {
	fm := FuncMap()
	tmpl := template.Must(template.New("test").Funcs(fm).Parse(`{{ "hello" | truncate 10 }}`))
	assert.NotNil(t, tmpl)
}

func TestDefaultValue_Nil(t *testing.T) {
	assert.Equal(t, "fallback", defaultValue("fallback", nil))
}

func TestDefaultValue_NotNil(t *testing.T) {
	assert.Equal(t, "actual", defaultValue("fallback", "actual"))
}

func TestDefaultValue_ZeroValue(t *testing.T) {
	assert.Equal(t, 0, defaultValue(42, 0))
}

func TestAdd1(t *testing.T) {
	assert.Equal(t, 6, add1(5))
	assert.Equal(t, 1, add1(0))
	assert.Equal(t, 0, add1(-1))
}

func TestSub(t *testing.T) {
	assert.Equal(t, 3, sub(5, 2))
	assert.Equal(t, -3, sub(2, 5))
	assert.Equal(t, 0, sub(0, 0))
}

func TestSubtract(t *testing.T) {
	assert.Equal(t, 10, sub(20, 10))
}

func TestAdd(t *testing.T) {
	assert.Equal(t, 7, add(3, 4))
	assert.Equal(t, 0, add(-1, 1))
}

func TestLoop(t *testing.T) {
	assert.Equal(t, []int{1, 2, 3}, loop(1, 3))
}

func TestLoop_SingleElement(t *testing.T) {
	assert.Equal(t, []int{5}, loop(5, 5))
}

func TestLoop_Zero(t *testing.T) {
	// loop(0, 0) returns [0]
	assert.Equal(t, []int{0}, loop(0, 0))
}

func TestLoop_Descending(t *testing.T) {
	// Should still go forward from start to end
	assert.Equal(t, []int{3, 4, 5}, loop(3, 5))
}

func TestFormatBytes_Bytes(t *testing.T) {
	assert.Equal(t, "500 B", formatBytes(500))
}

func TestFormatBytes_KB(t *testing.T) {
	assert.Equal(t, "1.0 KB", formatBytes(1024))
}

func TestFormatBytes_MB(t *testing.T) {
	assert.Equal(t, "1.0 MB", formatBytes(1024*1024))
}

func TestFormatBytes_GB(t *testing.T) {
	assert.Equal(t, "1.0 GB", formatBytes(1024*1024*1024))
}

func TestFormatBytes_Large(t *testing.T) {
	assert.Equal(t, "1.5 MB", formatBytes(1572864)) // 1.5 * 1024 * 1024
}

func TestCsrfToken(t *testing.T) {
	assert.Equal(t, "{{ .csrf }}", csrfToken())
}

func TestTruncate_Shorter(t *testing.T) {
	assert.Equal(t, "hello", truncate("hello", 10))
}

func TestTruncate_Exact(t *testing.T) {
	assert.Equal(t, "hello", truncate("hello", 5))
}

func TestTruncate_Longer(t *testing.T) {
	assert.Equal(t, "hello...", truncate("hello world", 5))
}

func TestTruncate_Unicode(t *testing.T) {
	assert.Equal(t, "привет...", truncate("привет мир", 6))
}

func TestTruncate_Zero(t *testing.T) {
	assert.Equal(t, "...", truncate("hello", 0))
}

// H1 (pass 30): formatDate/formatDateTime должны принимать *time.Time.
func TestFormatDate_TimeValue(t *testing.T) {
	ts := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	assert.Equal(t, "08.08.2026", formatDate("ru", 0, ts))
	assert.Equal(t, "08 Aug 2026", formatDate("en", 0, ts))
}

func TestFormatDate_Pointer(t *testing.T) {
	ts := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	assert.Equal(t, "08.08.2026", formatDate("ru", 0, &ts))
	assert.Equal(t, "08 Aug 2026", formatDate("en", 0, &ts))
}

func TestFormatDate_NilPointer(t *testing.T) {
	assert.Equal(t, "", formatDate("ru", 0, (*time.Time)(nil)))
}

func TestFormatDate_Invalid(t *testing.T) {
	assert.Equal(t, "", formatDate("ru", 0, "not a time"))
	assert.Equal(t, "", formatDate("ru", 0, nil))
}

func TestFormatDateTime_Pointer(t *testing.T) {
	ts := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	assert.Equal(t, "08.08.2026 12:00", formatDateTime("ru", 0, &ts))
	assert.Equal(t, "08 Aug 2026 12:00", formatDateTime("en", 0, &ts))
	assert.Equal(t, "", formatDateTime("ru", 0, (*time.Time)(nil)))
}

// M5 (pass 30): tzOffset сдвигает время на минуты от UTC.
func TestFormatDateTime_TZOffset(t *testing.T) {
	ts := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	// UTC+3 → 15:00
	assert.Equal(t, "08.08.2026 15:00", formatDateTime("ru", 180, &ts))
	// UTC-5 → 07:00
	assert.Equal(t, "08.08.2026 07:00", formatDateTime("ru", -300, &ts))
	// Нулевой offset → UTC
	assert.Equal(t, "08.08.2026 12:00", formatDateTime("ru", 0, &ts))
	// float64 (из gin.H) тоже поддерживается
	assert.Equal(t, "08.08.2026 15:00", formatDateTime("ru", float64(180), &ts))
}
