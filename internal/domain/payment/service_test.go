// internal/domain/payment/service_test.go
// G-1..G-3 (pass 45): тесты платежей.
package payment

import (
	"testing"
)

// G-3 (pass 45): проверка IP-allowlist ЮKassa.
func TestIsYooKassaIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"185.71.76.10", true},
		{"185.71.77.5", true},
		{"77.75.153.10", true},
		{"8.8.8.8", false},
		{"192.168.0.1", false},
		{"not-an-ip", false},
	}
	for _, tc := range cases {
		got := isYooKassaIP(tc.ip)
		if got != tc.want {
			t.Errorf("isYooKassaIP(%q) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}
