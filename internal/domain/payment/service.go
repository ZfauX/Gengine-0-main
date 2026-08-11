// internal/domain/payment/service.go
// G-1..G-3 (pass 45): сервис платежей ЮKassa.
package payment

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/notification"

	"github.com/rs/zerolog/log"
)

// YooKassaAPI — базовый URL API ЮKassa.
const YooKassaAPI = "https://api.yookassa.ru/v3"

// yooKassaIPRanges — IP-адреса, с которых ЮKassa шлёт вебхуки.
var yooKassaIPRanges = []string{
	"185.71.76.0/27",
	"185.71.77.0/27",
	"77.75.153.0/25",
	"77.75.154.128/25",
	"2a02:5180::/32",
}

// PaymentService — работа с платежами ЮKassa.
type PaymentService struct {
	cfg    config.PaymentConfig
	repo   PaymentRepository
	client *http.Client

	// notifier (IDEA-7): опциональный сервис уведомлений — при успешной оплате
	// пользователю создаётся уведомление «Платёж подтверждён». Настраивается
	// через WithNotificationService; nil — уведомления не шлются.
	notifier PaymentNotifier
}

// PaymentNotifier — минимальный контракт для создания уведомления пользователю
// (избегаем жёсткой зависимости payment→notification).
type PaymentNotifier interface {
	Create(ctx context.Context, userID uint, ntype notification.NotificationType, title, body, link string) error
}

func NewPaymentService(cfg config.PaymentConfig, repo PaymentRepository) *PaymentService {
	return &PaymentService{
		cfg:    cfg,
		repo:   repo,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// WithNotificationService внедряет сервис уведомлений (IDEA-7).
func (s *PaymentService) WithNotificationService(notifier PaymentNotifier) *PaymentService {
	s.notifier = notifier
	return s
}

// Enabled возвращает true, если платёжная функция настроена.
func (s *PaymentService) Enabled() bool {
	return s.cfg.ShopID != "" && s.cfg.SecretKey != ""
}

// newIdempotencyKey генерирует случайный ключ идемпотентности.
func newIdempotencyKey() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// isYooKassaIP проверяет, что запрос пришёл с IP ЮKassa.
func isYooKassaIP(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, cidr := range yooKassaIPRanges {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(parsed) {
			return true
		}
	}
	return false
}

// yooKassaCreatePayment — запрос к ЮKassa на создание платежа.
func (s *PaymentService) yooKassaCreatePayment(ctx context.Context, amount float64, currency, description, returnURL, idempotencyKey string) (map[string]any, error) {
	payload := map[string]any{
		"amount": map[string]any{
			"value":    fmt.Sprintf("%.2f", amount),
			"currency": currency,
		},
		"capture":     true,
		"description": description,
		"confirmation": map[string]any{
			"type":       "redirect",
			"return_url": returnURL,
		},
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, YooKassaAPI+"/payments", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(s.cfg.ShopID, s.cfg.SecretKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotence-Key", idempotencyKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("yookassa: create payment failed (%d): %s", resp.StatusCode, string(respBody))
	}
	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// yooKassaGetPayment — получение платежа из ЮKassa (подтверждение статуса).
func (s *PaymentService) yooKassaGetPayment(ctx context.Context, paymentID string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, YooKassaAPI+"/payments/"+paymentID, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(s.cfg.ShopID, s.cfg.SecretKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yookassa: get payment failed (%d): %s", resp.StatusCode, string(respBody))
	}
	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// CreatePayment создаёт платёж и возвращает URL подтверждения.
// DEEP-REVIEW HIGH #7 (pass 46): запись сохраняется в БД ДО вызова ЮKassa с
// временным PaymentID="local-"+idemKey. Если вызов API упадёт, повторный
// CreatePayment с тем же idempotency-ключом вернёт СУЩЕСТВУЮЩУЮ запись —
// второй платёж в ЮKassa не создаётся (раньше БД-запись терялась и при
// retry создавался дубликат платежа).
func (s *PaymentService) CreatePayment(ctx context.Context, userID uint, amount float64, description, metadata string) (*Payment, string, error) {
	if !s.Enabled() {
		return nil, "", fmt.Errorf("платежи не настроены")
	}
	idemKey, err := newIdempotencyKey()
	if err != nil {
		return nil, "", err
	}

	// Retry после сбоя БД/API: запись уже существует — возвращаем её.
	if existing, findErr := s.repo.GetByIdempotencyKey(ctx, idemKey); findErr == nil && existing != nil {
		log.Info().Uint("payment", existing.ID).Uint("user_id", existing.UserID).Msg("CreatePayment: returning existing pending payment (idempotency)")
		return existing, existing.ConfirmationURL, nil
	}

	currency := s.cfg.Currency
	if currency == "" {
		currency = "RUB"
	}
	returnURL := s.cfg.ReturnURL

	// 1. Сохраняем запись pending ДО вызова API.
	p := &Payment{
		UserID:         userID,
		PaymentID:      "local-" + idemKey,
		IdempotencyKey: idemKey,
		Amount:         amount,
		Currency:       currency,
		Description:    description,
		Status:         StatusPending,
		Metadata:       metadata,
	}
	if createErr := s.repo.Create(ctx, p); createErr != nil {
		return nil, "", createErr
	}

	// 2. Создаём платёж в ЮKassa (идемпотентность по ключу).
	result, err := s.yooKassaCreatePayment(ctx, amount, currency, description, returnURL, idemKey)
	if err != nil {
		log.Error().Err(err).Uint("payment", p.ID).Msg("CreatePayment: YooKassa call failed, leaving pending record for retry")
		return nil, "", err
	}

	paymentID, _ := result["id"].(string)
	status, _ := result["status"].(string)
	confirmationURL := ""
	if conf, ok := result["confirmation"].(map[string]any); ok {
		confirmationURL, _ = conf["confirmation_url"].(string)
	}
	if status == "" {
		status = StatusPending
	}

	// 3. Обновляем запись реальным payment_id/статусом/URL.
	if err := s.repo.UpdateAfterCreate(ctx, p.ID, paymentID, status, confirmationURL); err != nil {
		// Не критично для возврата — запись останется с local- id, но
		// webhook подтвердит по payment_id из ЮKassa.
		log.Error().Err(err).Uint("payment", p.ID).Str("payment_id", paymentID).Msg("CreatePayment: failed to update record with real payment id")
	}
	p.PaymentID = paymentID
	p.Status = status
	p.ConfirmationURL = confirmationURL
	return p, confirmationURL, nil
}

// HandleWebhook обрабатывает уведомление от ЮKassa.
// Аутентификация: IP-allowlist ЮKassa + подтверждение статуса через API.
func (s *PaymentService) HandleWebhook(ctx context.Context, remoteIP string, rawBody []byte) error {
	if !s.Enabled() {
		return fmt.Errorf("платежи не настроены")
	}
	if !isYooKassaIP(remoteIP) {
		return fmt.Errorf("%w: %s", ErrWebhookUntrustedIP, remoteIP)
	}

	var notif struct {
		Event  string         `json:"event"`
		Object map[string]any `json:"object"`
	}
	if err := json.Unmarshal(rawBody, &notif); err != nil {
		return fmt.Errorf("%w: %v", ErrWebhookInvalid, err)
	}
	paymentID, _ := notif.Object["id"].(string)
	if paymentID == "" {
		return fmt.Errorf("%w: missing payment id", ErrWebhookInvalid)
	}

	// Подтверждаем статус через API (самый надёжный способ против подделки).
	remote, err := s.yooKassaGetPayment(ctx, paymentID)
	if err != nil {
		return err
	}
	status, _ := remote["status"].(string)

	local, err := s.repo.GetByPaymentID(ctx, paymentID)
	if err != nil {
		return fmt.Errorf("webhook: payment not found: %w", err)
	}

	// Приводим к нашему статусу.
	switch status {
	case "succeeded":
		// DEEP-REVIEW (pass 46): верифицируем сумму и валюту из API против
		// локальной записи. Раньше статус подтверждался, но не сумма —
		// partially-captured/несовпадающий платёж мог быть помечен succeeded.
		amountOK, amountErr := s.verifyRemoteAmount(remote, local)
		if amountErr != nil {
			return amountErr
		}
		if !amountOK {
			log.Error().
				Uint("payment", local.ID).
				Float64("local_amount", local.Amount).
				Str("currency", local.Currency).
				Interface("remote_amount", remote["amount"]).
				Msg("webhook: amount/currency mismatch, rejecting")
			return fmt.Errorf("webhook: amount/currency mismatch for payment %s", paymentID)
		}
		if err := s.repo.UpdateStatus(ctx, local.ID, StatusSucceeded); err != nil {
			return err
		}
		log.Info().Uint("payment", local.ID).Uint("user_id", local.UserID).Float64("amount", local.Amount).Msg("payment succeeded")
		s.notifyPaymentSucceeded(ctx, local)
	case "canceled":
		if err := s.repo.UpdateStatus(ctx, local.ID, StatusCanceled); err != nil {
			return err
		}
	default:
		// waiting_for_capture / pending — без изменений.
	}
	return nil
}

// notifyPaymentSucceeded (IDEA-7): уведомляем пользователя о подтверждении
// платежа (WebSocket + Web Push + запись в центре уведомлений). Ошибка не
// роняет вебхук — сам платёж уже подтверждён, уведомление не критично.
func (s *PaymentService) notifyPaymentSucceeded(ctx context.Context, local *Payment) {
	if s.notifier == nil {
		return
	}
	title := "Платёж подтверждён"
	body := fmt.Sprintf("Сумма %.2f %s", local.Amount, local.Currency)
	if err := s.notifier.Create(ctx, local.UserID, notification.NotificationTypeInfo, title, body, "/payments"); err != nil {
		log.Warn().Err(err).Uint("user_id", local.UserID).Uint("payment", local.ID).Msg("payment succeeded: notification create failed")
	}
}

// verifyRemoteAmount сверяет сумму/валюту из ответа ЮKassa с локальной записью.
// Допускает расхождение не более 1 копейки (арифметика float64).
func (s *PaymentService) verifyRemoteAmount(remote map[string]any, local *Payment) (bool, error) {
	amountRaw, ok := remote["amount"].(map[string]any)
	if !ok {
		return false, fmt.Errorf("webhook: missing amount in remote payment")
	}
	valueStr, _ := amountRaw["value"].(string)
	currency, _ := amountRaw["currency"].(string)

	remoteAmount, err := strconv.ParseFloat(valueStr, 64)
	if err != nil {
		return false, fmt.Errorf("webhook: invalid remote amount %q", valueStr)
	}
	if currency != local.Currency {
		return false, nil
	}
	// Допуск 0.01 (копейка) — ЮKassa отдаёт «100.00», float64-арифметика.
	return math.Abs(remoteAmount-local.Amount) < 0.011, nil
}

// ListByUser возвращает платежи пользователя.
func (s *PaymentService) ListByUser(ctx context.Context, userID uint, limit int) ([]Payment, error) {
	return s.repo.ListByUser(ctx, userID, limit)
}
