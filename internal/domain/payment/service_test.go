// internal/domain/payment/service_test.go
// G-1..G-3 (pass 45): тесты платежей.
package payment

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/notification"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// DEEP-REVIEW (pass 46): верификация суммы/валюты вебхука.
func TestVerifyRemoteAmount(t *testing.T) {
	svc := &PaymentService{}

	// Совпадает.
	ok, err := svc.verifyRemoteAmount(
		map[string]any{"amount": map[string]any{"value": "100.00", "currency": "RUB"}},
		&Payment{Amount: 100, Currency: "RUB"},
	)
	require.NoError(t, err)
	assert.True(t, ok)

	// Несовпадение суммы — отклонить.
	ok, err = svc.verifyRemoteAmount(
		map[string]any{"amount": map[string]any{"value": "50.00", "currency": "RUB"}},
		&Payment{Amount: 100, Currency: "RUB"},
	)
	require.NoError(t, err)
	assert.False(t, ok)

	// Несовпадение валюты — отклонить.
	ok, err = svc.verifyRemoteAmount(
		map[string]any{"amount": map[string]any{"value": "100.00", "currency": "USD"}},
		&Payment{Amount: 100, Currency: "RUB"},
	)
	require.NoError(t, err)
	assert.False(t, ok)

	// Отсутствие amount — ошибка.
	_, err = svc.verifyRemoteAmount(map[string]any{}, &Payment{Amount: 100, Currency: "RUB"})
	require.Error(t, err)
}

// stubNotifier — записывает вызовы Create для теста уведомлений.
type stubNotifier struct {
	calls []string
}

func (s *stubNotifier) Create(ctx context.Context, userID uint, ntype notification.NotificationType, title, body, link string) error {
	s.calls = append(s.calls, fmt.Sprintf("%d:%s:%s", userID, ntype, title))
	return nil
}

// IDEA-7: при подтверждении платежа создаётся уведомление пользователю.
func TestNotifyPaymentSucceeded(t *testing.T) {
	t.Run("notifier внедрён — уведомление создаётся", func(t *testing.T) {
		stub := &stubNotifier{}
		svc := &PaymentService{notifier: stub}

		svc.notifyPaymentSucceeded(context.Background(), &Payment{ID: 1, UserID: 42, Amount: 100, Currency: "RUB"})

		require.Len(t, stub.calls, 1)
		assert.Equal(t, "42:info:Платёж подтверждён", stub.calls[0])
	})

	t.Run("notifier nil — ничего не делаем", func(t *testing.T) {
		svc := &PaymentService{}
		svc.notifyPaymentSucceeded(context.Background(), &Payment{ID: 1, UserID: 42, Amount: 100, Currency: "RUB"})
		// Никакого panic — просто no-op.
	})

	t.Run("WithNotificationService внедряет notifier", func(t *testing.T) {
		stub := &stubNotifier{}
		svc := NewPaymentService(config.PaymentConfig{}, nil).WithNotificationService(stub)
		assert.NotNil(t, svc.notifier)
	})
}
