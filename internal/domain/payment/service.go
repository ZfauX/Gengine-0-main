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
	"net"
	"net/http"
	"time"

	"gengine-0/internal/config"

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
}

func NewPaymentService(cfg config.PaymentConfig, repo PaymentRepository) *PaymentService {
	return &PaymentService{
		cfg:    cfg,
		repo:   repo,
		client: &http.Client{Timeout: 15 * time.Second},
	}
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
func (s *PaymentService) CreatePayment(ctx context.Context, userID uint, amount float64, description, metadata string) (*Payment, string, error) {
	if !s.Enabled() {
		return nil, "", fmt.Errorf("платежи не настроены")
	}
	idemKey, err := newIdempotencyKey()
	if err != nil {
		return nil, "", err
	}

	currency := s.cfg.Currency
	if currency == "" {
		currency = "RUB"
	}
	returnURL := s.cfg.ReturnURL

	result, err := s.yooKassaCreatePayment(ctx, amount, currency, description, returnURL, idemKey)
	if err != nil {
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

	p := &Payment{
		UserID:          userID,
		PaymentID:       paymentID,
		IdempotencyKey:  idemKey,
		Amount:          amount,
		Currency:        currency,
		Description:     description,
		Status:          status,
		ConfirmationURL: confirmationURL,
		Metadata:        metadata,
	}
	if err := s.repo.Create(ctx, p); err != nil {
		return nil, "", err
	}
	return p, confirmationURL, nil
}

// HandleWebhook обрабатывает уведомление от ЮKassa.
// Аутентификация: IP-allowlist ЮKassa + подтверждение статуса через API.
func (s *PaymentService) HandleWebhook(ctx context.Context, remoteIP string, rawBody []byte) error {
	if !s.Enabled() {
		return fmt.Errorf("платежи не настроены")
	}
	if !isYooKassaIP(remoteIP) {
		return fmt.Errorf("webhook from untrusted IP: %s", remoteIP)
	}

	var notif struct {
		Event  string         `json:"event"`
		Object map[string]any `json:"object"`
	}
	if err := json.Unmarshal(rawBody, &notif); err != nil {
		return fmt.Errorf("webhook: invalid body: %w", err)
	}
	paymentID, _ := notif.Object["id"].(string)
	if paymentID == "" {
		return fmt.Errorf("webhook: missing payment id")
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
		if err := s.repo.UpdateStatus(ctx, local.ID, StatusSucceeded); err != nil {
			return err
		}
		log.Info().Uint("payment", local.ID).Uint("user_id", local.UserID).Float64("amount", local.Amount).Msg("payment succeeded")
	case "canceled":
		if err := s.repo.UpdateStatus(ctx, local.ID, StatusCanceled); err != nil {
			return err
		}
	default:
		// waiting_for_capture / pending — без изменений.
	}
	return nil
}

// ListByUser возвращает платежи пользователя.
func (s *PaymentService) ListByUser(ctx context.Context, userID uint, limit int) ([]Payment, error) {
	return s.repo.ListByUser(ctx, userID, limit)
}
