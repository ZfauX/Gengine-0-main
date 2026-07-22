package sanitize

import (
	"testing"
)

func FuzzStripHTML(f *testing.F) {
	seeds := []string{
		"<b>bold</b>",
		"<script>alert('xss')</script>",
		"<a href='javascript:alert(1)'>click</a>",
		"<img src=x onerror=alert(1)>",
		"normal text",
		"",
		"<p>Safe HTML</p>",
		"<svg onload=alert(1)>",
		"plain text with < and > chars",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		result := StripHTML(input)
		if len(result) > len(input)+len(input)/2 {
			t.Errorf("StripHTML produced unexpectedly long output for input %q (len=%d): %q (len=%d)",
				input, len(input), result, len(result))
		}
	})
}
