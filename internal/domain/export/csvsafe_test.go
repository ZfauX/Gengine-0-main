// internal/domain/export/csvsafe_test.go
package export

import "testing"

// TestCsvSafe проверяет нейтрализацию CSV/Excel formula injection (T-2, pass 37).
func TestCsvSafe(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"plain", "Level 1", "Level 1"},
		{"equals", "=cmd()", "'=cmd()"},
		{"plus", "+SUM(A1)", "'+SUM(A1)"},
		{"minus", "-2+2", "'-2+2"},
		{"at", "@user", "'@user"},
		{"tab", "\t=cmd", "'\t=cmd"},
		{"cr", "\r=cmd", "'\r=cmd"},
		// L-1 (pass 37): ведущий пробел перед формулой тоже опасен.
		{"space-equals", " =2+2", "' =2+2"},
		{"spaces-equals", "   =cmd()", "'   =cmd()"},
		{"space-plain", " normal", " normal"},
		{"unicode-safe", "«Вопрос»", "«Вопрос»"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := csvSafe(tt.input); got != tt.want {
				t.Errorf("csvSafe(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
