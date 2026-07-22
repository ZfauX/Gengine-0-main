package validation

import (
	"testing"
)

func FuzzValidateEmail(f *testing.F) {
	seeds := []string{
		"test@example.com",
		"user+tag@domain.co.uk",
		"invalid-email",
		"@domain.com",
		"",
		"a@b.c",
		"test@.com",
		"test@domain",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, email string) {
		_ = ValidateEmail(email)
	})
}

func FuzzValidateString(f *testing.F) {
	seeds := []string{
		"hello",
		"",
		"   ",
		"a",
		"<script>alert(1)</script>",
		"ユニコード",
		"../../etc/passwd",
		"normal-string-123",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, s string) {
		_ = ValidateString("field", s, 2, 100)
	})
}
