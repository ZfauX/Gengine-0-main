// internal/domain/payment/service_test.go
// G-1..G-3 (pass 45): тесты платежей.
package payment

import (
	"context"
	"encoding/base64"
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

// DEEP-REVIEW (pass 46) + M10 (PASS-3): верификация суммы/валюты вебхука.
// Теперь суммы в КОПЕЙКАХ и сравнение ТОЧНОЕ (без float64-допуска).
func TestVerifyRemoteAmount(t *testing.T) {
	svc := &PaymentService{}

	// Совпадает.
	ok, err := svc.verifyRemoteAmount(
		map[string]any{"amount": map[string]any{"value": "100.00", "currency": "RUB"}},
		&Payment{AmountKopecks: 10000, Currency: "RUB"},
	)
	require.NoError(t, err)
	assert.True(t, ok)

	// Несовпадение суммы — отклонить.
	ok, err = svc.verifyRemoteAmount(
		map[string]any{"amount": map[string]any{"value": "50.00", "currency": "RUB"}},
		&Payment{AmountKopecks: 10000, Currency: "RUB"},
	)
	require.NoError(t, err)
	assert.False(t, ok)

	// Несовпадение валюты — отклонить.
	ok, err = svc.verifyRemoteAmount(
		map[string]any{"amount": map[string]any{"value": "100.00", "currency": "USD"}},
		&Payment{AmountKopecks: 10000, Currency: "RUB"},
	)
	require.NoError(t, err)
	assert.False(t, ok)

	// M10: «0.29» должно парситься точно в 29 копеек (float64 дал бы 28).
	ok, err = svc.verifyRemoteAmount(
		map[string]any{"amount": map[string]any{"value": "0.29", "currency": "RUB"}},
		&Payment{AmountKopecks: 29, Currency: "RUB"},
	)
	require.NoError(t, err)
	assert.True(t, ok, "0.29 рубля == 29 копеек (точное сравнение)")

	// Отсутствие amount — ошибка.
	_, err = svc.verifyRemoteAmount(map[string]any{}, &Payment{AmountKopecks: 10000, Currency: "RUB"})
	require.Error(t, err)
}

// M10: преобразования копеек ↔ строки.
func TestKopecksConversions(t *testing.T) {
	cases := []struct {
		kopecks int64
		want    string
	}{
		{0, "0.00"},
		{1, "0.01"},
		{99, "0.99"},
		{100, "1.00"},
		{10000, "100.00"},
		{10001, "100.01"},
		{-1, "-0.01"},
		{-10001, "-100.01"},
	}
	for _, tc := range cases {
		got := kopecksToRublesString(tc.kopecks)
		assert.Equal(t, tc.want, got, "kopecksToRublesString(%d)", tc.kopecks)

		parsed, err := rublesStringToKopecks(tc.want)
		require.NoError(t, err)
		assert.Equal(t, tc.kopecks, parsed, "roundtrip %s", tc.want)
	}

	// Дробная часть без ведущего нуля: «100.5» → 10050.
	k, err := rublesStringToKopecks("100.5")
	require.NoError(t, err)
	assert.Equal(t, int64(10050), k)

	// Больше 2 знаков — ошибка.
	_, err = rublesStringToKopecks("1.234")
	require.Error(t, err)

	// Целые рубли без точки.
	k, err = rublesStringToKopecks("50")
	require.NoError(t, err)
	assert.Equal(t, int64(5000), k)

	// rublesToKopecks из формы.
	assert.Equal(t, int64(10000), rublesToKopecks(100.0))
	assert.Equal(t, int64(29), rublesToKopecks(0.29))
}

// H1 (PASS-4): верификация Authorization (Basic ShopID:WebhookKey) вебхука.
// ЮKassa НЕ шлёт Basic — заголовок опционален; если есть и неверен → отклоняем.
func TestVerifyWebhookAuth(t *testing.T) {
	svc := NewPaymentService(config.PaymentConfig{
		ShopID:     "shop_123",
		SecretKey:  "secret",
		WebhookKey: "hookkey",
	}, nil)

	mkAuth := func(user, pass string) string {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	}

	t.Run("валидный WebhookKey", func(t *testing.T) {
		ok, err := svc.verifyWebhookAuth(mkAuth("shop_123", "hookkey"))
		require.NoError(t, err)
		assert.True(t, ok)
	})
	t.Run("пустой заголовок = легитимный вебхук ЮKassa", func(t *testing.T) {
		// По документации ЮKassa вебхуки приходят БЕЗ Basic — пропускаем.
		ok, err := svc.verifyWebhookAuth("")
		require.NoError(t, err)
		assert.True(t, ok)
	})
	t.Run("не Basic", func(t *testing.T) {
		ok, err := svc.verifyWebhookAuth("Bearer xyz")
		require.ErrorIs(t, err, ErrWebhookUnauthorized)
		assert.False(t, ok)
	})
	t.Run("неверный пароль", func(t *testing.T) {
		ok, err := svc.verifyWebhookAuth(mkAuth("shop_123", "wrong"))
		require.ErrorIs(t, err, ErrWebhookUnauthorized)
		assert.False(t, ok)
	})
	t.Run("неверный shopId", func(t *testing.T) {
		ok, err := svc.verifyWebhookAuth(mkAuth("other", "hookkey"))
		require.ErrorIs(t, err, ErrWebhookUnauthorized)
		assert.False(t, ok)
	})
	t.Run("битый base64", func(t *testing.T) {
		ok, err := svc.verifyWebhookAuth("Basic !!!")
		require.ErrorIs(t, err, ErrWebhookUnauthorized)
		assert.False(t, ok)
	})

	// Если WebhookKey не задан — используем SecretKey.
	svcNoHook := NewPaymentService(config.PaymentConfig{ShopID: "shop_123", SecretKey: "secret"}, nil)
	t.Run("fallback на SecretKey", func(t *testing.T) {
		ok, err := svcNoHook.verifyWebhookAuth(mkAuth("shop_123", "secret"))
		require.NoError(t, err)
		assert.True(t, ok)
	})
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

		svc.notifyPaymentSucceeded(context.Background(), &Payment{ID: 1, UserID: 42, AmountKopecks: 10000, Currency: "RUB"})

		require.Len(t, stub.calls, 1)
		assert.Equal(t, "42:info:Платёж подтверждён", stub.calls[0])
	})

	t.Run("notifier nil — ничего не делаем", func(t *testing.T) {
		svc := &PaymentService{}
		svc.notifyPaymentSucceeded(context.Background(), &Payment{ID: 1, UserID: 42, AmountKopecks: 10000, Currency: "RUB"})
		// Никакого panic — просто no-op.
	})

	t.Run("WithNotificationService внедряет notifier", func(t *testing.T) {
		stub := &stubNotifier{}
		svc := NewPaymentService(config.PaymentConfig{}, nil).WithNotificationService(stub)
		assert.NotNil(t, svc.notifier)
	})
}
