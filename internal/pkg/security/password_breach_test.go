package security

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
	"testing"
)

func TestValidatePasswordWithBreachCheck_TooShort(t *testing.T) {
	err := ValidatePasswordWithBreachCheck("short")
	if err == nil {
		t.Fatal("expected error for short password")
	}
	if !strings.Contains(err.Error(), "at least 8 characters") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidatePasswordWithBreachCheck_TooLong(t *testing.T) {
	long := strings.Repeat("a", 200)
	err := ValidatePasswordWithBreachCheck(long)
	if err == nil {
		t.Fatal("expected error for long password")
	}
	if !strings.Contains(err.Error(), "too long") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestCheckPasswordBreach_Empty(t *testing.T) {
	safe, count, err := CheckPasswordBreach("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !safe {
		t.Error("expected empty password to be safe")
	}
	if count != 0 {
		t.Errorf("expected count 0, got %d", count)
	}
}

func TestSHA1HashStructure(t *testing.T) {
	password := "testpassword123"
	hash := sha1.Sum([]byte(password))
	hashStr := hex.EncodeToString(hash[:])

	if len(hashStr) != 40 {
		t.Errorf("expected SHA-1 hash length 40, got %d", len(hashStr))
	}

	prefix := hashStr[:5]
	suffix := hashStr[5:]

	if len(prefix) != 5 {
		t.Errorf("expected prefix length 5, got %d", len(prefix))
	}
	if len(suffix) != 35 {
		t.Errorf("expected suffix length 35, got %d", len(suffix))
	}

	// Verify the HIBP API format: first 5 hex chars as prefix, rest as suffix
	if !strings.HasPrefix(hashStr, prefix) {
		t.Error("hash should start with prefix")
	}
	if !strings.HasSuffix(hashStr, suffix) {
		t.Error("hash should end with suffix")
	}
}

func TestValidatePasswordWithBreachCheck_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{"empty", "", true},
		{"exactly 8 chars", "12345678", false},
		{"exactly 128 chars", strings.Repeat("a", 128), false},
		{"129 chars", strings.Repeat("a", 129), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePasswordWithBreachCheck(tt.password)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
