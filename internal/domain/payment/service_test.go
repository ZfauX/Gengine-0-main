// internal/domain/payment/service_test.go
// G-1..G-3 (pass 45): тесты платежей.
package payment

import (
	"errors"
	"fmt"
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

// S-46-3 (pass 46): классификация ошибок вебхука для HTTP-статусов.
func TestWebhookErrorClassification(t *testing.T) {
	// Недоверенный IP — ErrWebhookUntrustedIP (403).
	err := fmt.Errorf("%w: 1.2.3.4", ErrWebhookUntrustedIP)
	if !errors.Is(err, ErrWebhookUntrustedIP) {
		t.Error("expected ErrWebhookUntrustedIP to match")
	}
	if errors.Is(err, ErrWebhookInvalid) {
		t.Error("untrusted IP error should not be ErrWebhookInvalid")
	}

	// Некорректное тело — ErrWebhookInvalid (400).
	err2 := fmt.Errorf("%w: bad json", ErrWebhookInvalid)
	if !errors.Is(err2, ErrWebhookInvalid) {
		t.Error("expected ErrWebhookInvalid to match")
	}

	// Временная ошибка (не sentinel) — не 4xx, ЮKassa ретраит.
	err3 := errors.New("yookassa: get payment failed (500)")
	if errors.Is(err3, ErrWebhookInvalid) || errors.Is(err3, ErrWebhookUntrustedIP) {
		t.Error("transient error should not match sentinel errors")
	}
}
