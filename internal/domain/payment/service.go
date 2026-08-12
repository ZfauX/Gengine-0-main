// internal/domain/payment/service.go
// G-1..G-3 (pass 45): сервис платежей ЮKassa.
package payment

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
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

// kopecksToRublesString (M10): 10000 копеек → "100.00" (формат ЮKassa).
// Целочисленная арифметика — без погрешностей float64.
func kopecksToRublesString(kopecks int64) string {
	negative := kopecks < 0
	if negative {
		kopecks = -kopecks
	}
	whole := kopecks / 100
	frac := kopecks % 100
	if negative {
		return fmt.Sprintf("-%d.%02d", whole, frac)
	}
	return fmt.Sprintf("%d.%02d", whole, frac)
}

// rublesToKopecks (M10): рубли (float64 из формы) → копейки с округлением.
// Используется ТОЛЬКО на входе из user-интерфейса (форма amount в рублях);
// внутри системы все суммы — int64-копейки.
func rublesToKopecks(rubles float64) int64 {
	return int64(math.Round(rubles * 100))
}

// rublesStringToKopecks (M10): «100.00» → 10000 копеек. Строго: парсим целую
// и дробную часть без float64 (иначе «0.29» → 28 копеек из-за бинарного
// представления). Допускаются 1-2 знака после точки.
func rublesStringToKopecks(s string) (int64, error) {
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	whole, frac, ok := strings.Cut(s, ".")
	if !ok {
		// Без дробной части — целые рубли.
		w, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, err
		}
		if neg {
			w = -w
		}
		// L1 (PASS-5): защита от переполнения w*100 (|w| > MaxInt64/100).
		if w > math.MaxInt64/100 || w < math.MinInt64/100 {
			return 0, fmt.Errorf("сумма слишком велика: %q", s)
		}
		return w * 100, nil
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, err
	}
	// Дробная часть: до 2 знаков, недостающие — дополняем нулями.
	switch len(frac) {
	case 0:
		frac = "00"
	case 1:
		frac += "0"
	case 2:
	default:
		return 0, fmt.Errorf("too many decimal places: %q", frac)
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, err
	}
	// L1 (PASS-5): проверка до умножения — w*100 не должно переполняться.
	// f≤99 не влияет на границу; MinInt64-f отдельно не проверяем (переполнение).
	if w > math.MaxInt64/100 || w < math.MinInt64/100 {
		return 0, fmt.Errorf("сумма слишком велика: %q", whole)
	}
	result := w*100 + f
	if neg {
		result = -result
	}
	return result, nil
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
// DEEP-REVIEW PASS-3 M10: amountKopecks (int64) → строка «рубли.копейки».
func (s *PaymentService) yooKassaCreatePayment(ctx context.Context, amountKopecks int64, currency, description, returnURL, idempotencyKey string) (map[string]any, error) {
	payload := map[string]any{
		"amount": map[string]any{
			// «100.00» из 10000 копеек; точное целочисленное преобразование.
			"value":    kopecksToRublesString(amountKopecks),
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
// DEEP-REVIEW PASS-3 M10: amountKopecks (int64) — денежная арифметика без float64.
// DEEP-REVIEW HIGH #7 (pass 46): запись сохраняется в БД ДО вызова ЮKassa с
// временным PaymentID="local-"+idemKey. Если вызов API упадёт, повторный
// CreatePayment с тем же idempotency-ключом вернёт СУЩЕСТВУЮЩУЮ запись —
// второй платёж в ЮKassa не создаётся (раньше БД-запись терялась и при
// retry создавался дубликат платежа).
func (s *PaymentService) CreatePayment(ctx context.Context, userID uint, amountKopecks int64, description, metadata string) (*Payment, string, error) {
	if !s.Enabled() {
		return nil, "", fmt.Errorf("платежи не настроены")
	}

	// DEEP-REVIEW PASS-5 H1: идемпотентность. Раньше idemKey генерировался
	// заново на каждый вызов — GetByIdempotencyKey по свежему ключу никогда
	// не находил запись, и ретрай создавал ДУБЛИКАТ платежа в ЮKassa.
	// Теперь переиспользуем существующий pending-платёж пользователя на ту же
	// сумму (retry после сбоя API/БД возвращает прежнюю запись).
	if existing, findErr := s.repo.GetPendingByUserAndAmount(ctx, userID, amountKopecks); findErr == nil && existing != nil {
		log.Info().Uint("payment", existing.ID).Uint("user_id", existing.UserID).Msg("CreatePayment: returning existing pending payment (idempotency)")
		// Если у существующей записи уже есть confirmation URL — отдаём его;
		// иначе пробуем довызвать ЮKassa ниже.
		if existing.ConfirmationURL != "" {
			return existing, existing.ConfirmationURL, nil
		}
		return s.resumePendingPayment(ctx, existing, description, metadata)
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

	// 1. Сохраняем запись pending ДО вызова API.
	p := &Payment{
		UserID:         userID,
		PaymentID:      "local-" + idemKey,
		IdempotencyKey: idemKey,
		AmountKopecks:  amountKopecks,
		Currency:       currency,
		Description:    description,
		Status:         StatusPending,
		Metadata:       metadata,
	}
	if createErr := s.repo.Create(ctx, p); createErr != nil {
		return nil, "", createErr
	}

	return s.createAtYooKassa(ctx, p, description, returnURL, idemKey)
}

// resumePendingPayment: существующий pending-платёж (из H1) — повторный вызов
// ЮKassa с тем же ключом идемпотентности (ЮKassa вернёт тот же платёж).
func (s *PaymentService) resumePendingPayment(ctx context.Context, p *Payment, description, metadata string) (*Payment, string, error) {
	returnURL := s.cfg.ReturnURL
	return s.createAtYooKassa(ctx, p, description, returnURL, p.IdempotencyKey)
}

// createAtYooKassa вызывает API и обновляет запись реальным payment_id.
func (s *PaymentService) createAtYooKassa(ctx context.Context, p *Payment, description, returnURL, idemKey string) (*Payment, string, error) {
	// 2. Создаём платёж в ЮKassa (идемпотентность по ключу).
	result, err := s.yooKassaCreatePayment(ctx, p.AmountKopecks, p.Currency, description, returnURL, idemKey)
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
		// DEEP-REVIEW PASS-5 H2: раньше «не критично» — но вебхук искал запись
		// по real payment_id, не находил "local-..." и платеж терялся.
		// Повторяем апдейт с fallback (запись всё ещё с "local-" id).
		log.Error().Err(err).Uint("payment", p.ID).Str("payment_id", paymentID).Msg("CreatePayment: failed to update record with real payment id, retrying")
		if retryErr := s.repo.UpdateAfterCreate(ctx, p.ID, paymentID, status, confirmationURL); retryErr != nil {
			log.Error().Err(retryErr).Uint("payment", p.ID).Str("payment_id", paymentID).Msg("CreatePayment: retry update also failed — webhook will not match; manual cleanup needed")
		}
	}
	p.PaymentID = paymentID
	p.Status = status
	p.ConfirmationURL = confirmationURL
	return p, confirmationURL, nil
}

// webhookEventStatus (L3, PASS-5): маппит event из тела вебхука на ожидаемый
// статус API. Пустая строка — неизвестный event (не проверяем).
func webhookEventStatus(event string) string {
	switch event {
	case "payment.succeeded":
		return "succeeded"
	case "payment.canceled":
		return "canceled"
	case "payment.waiting_for_capture":
		return "waiting_for_capture"
	default:
		return ""
	}
}

// verifyWebhookAuth проверяет Authorization заголовок вебхука ЮKassa, ЕСЛИ он
// присутствует (DEEP-REVIEW PASS-4 H1).
//
// ⚠️ ВАЖНО: по официальной документации ЮKassa («Notification authentication»)
// легитимные вебхуки аутентифицируются ТОЛЬКО по IP-адресам (yooKassaIPRanges)
// и проверке статуса объекта через API — ЮKassa НЕ отправляет Basic-заголовок
// в вебхуках. Поэтому эта проверка ОПЦИОНАЛЬНАЯ: если заголовок есть — сверяем
// (защита от подделки при широком TRUSTED_PROXIES); если его нет — пропускаем,
// полагаясь на IP-allowlist + yooKassaGetPayment + verifyRemoteAmount.
// Возвращает false, если заголовок ПРИСУТСТВУЕТ, но неверен (это попытка
// подделки), и true в остальных случаях (заголовка нет / заголовок верный).
func (s *PaymentService) verifyWebhookAuth(authHeader string) (bool, error) {
	if authHeader == "" {
		// Заголовка нет — легитимный вебхук ЮKassa (IP-allowlist ниже).
		return true, nil
	}
	const prefix = "Basic "
	if !strings.HasPrefix(authHeader, prefix) {
		return false, fmt.Errorf("%w: not basic", ErrWebhookUnauthorized)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(authHeader, prefix))
	if err != nil {
		return false, fmt.Errorf("%w: bad base64", ErrWebhookUnauthorized)
	}
	user, pass, ok := strings.Cut(string(decoded), ":")
	if !ok {
		return false, fmt.Errorf("%w: bad format", ErrWebhookUnauthorized)
	}
	expectedPass := s.cfg.WebhookKey
	if expectedPass == "" {
		expectedPass = s.cfg.SecretKey
	}
	if user != s.cfg.ShopID || pass != expectedPass {
		return false, ErrWebhookUnauthorized
	}
	return true, nil
}

// HandleWebhook обрабатывает уведомление от ЮKassa.
// Аутентификация (DEEP-REVIEW PASS-4 H1):
//  1. Optional Basic-подпись (если заголовок есть и неверен — отклонить);
//  2. IP-allowlist ЮKassa (основной фильтр, по документации ЮKassa);
//  3. подтверждение статуса через API + verifyRemoteAmount (финальная защита).
func (s *PaymentService) HandleWebhook(ctx context.Context, remoteIP, authHeader string, rawBody []byte) error {
	if !s.Enabled() {
		return fmt.Errorf("платежи не настроены")
	}
	// H1 (PASS-4): ЮKassa НЕ шлёт Basic в вебхуках — проверяем только если
	// заголовок ПРИСУТСТВУЕТ (защита от подделки при широком TRUSTED_PROXIES).
	// Пустой заголовок = легитимный вебхук (полагаемся на IP-allowlist ниже).
	authOK, authErr := s.verifyWebhookAuth(authHeader)
	if authErr != nil {
		return authErr
	}
	if !authOK {
		return ErrWebhookUnauthorized
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

	// L3 (PASS-5): сверяем event из тела вебхука с фактическим статусом из API.
	// Несовпадение = подделка/устаревшее уведомление — отклоняем (4xx, без ретраев).
	if expected := webhookEventStatus(notif.Event); expected != "" && expected != status {
		return fmt.Errorf("%w: event=%q but API status=%q", ErrWebhookEventMismatch, notif.Event, status)
	}

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
				Int64("local_amount_kopecks", local.AmountKopecks).
				Str("currency", local.Currency).
				Interface("remote_amount", remote["amount"]).
				Msg("webhook: amount/currency mismatch, rejecting")
			// L3 (PASS-5): sentinel → handler вернёт 4xx, ЮKassa перестанет
			// ретраить (раньше 500 → вечные ретраи + флуд API/логов).
			return fmt.Errorf("%w: payment %s", ErrWebhookAmountMismatch, paymentID)
		}
		if err := s.repo.UpdateStatus(ctx, local.ID, StatusSucceeded); err != nil {
			return err
		}
		log.Info().Uint("payment", local.ID).Uint("user_id", local.UserID).Int64("amount_kopecks", local.AmountKopecks).Msg("payment succeeded")
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
	body := fmt.Sprintf("Сумма %s %s", kopecksToRublesString(local.AmountKopecks), local.Currency)
	if err := s.notifier.Create(ctx, local.UserID, notification.NotificationTypeInfo, title, body, "/payments"); err != nil {
		log.Warn().Err(err).Uint("user_id", local.UserID).Uint("payment", local.ID).Msg("payment succeeded: notification create failed")
	}
}

// verifyRemoteAmount сверяет сумму/валюту из ответа ЮKassa с локальной записью.
// DEEP-REVIEW PASS-3 M10: строку «100.00» парсим в копейки и сравниваем ТОЧНО
// (int64) — без float64-допуска, который позволял расхождение ~1 копейки.
func (s *PaymentService) verifyRemoteAmount(remote map[string]any, local *Payment) (bool, error) {
	amountRaw, ok := remote["amount"].(map[string]any)
	if !ok {
		return false, fmt.Errorf("webhook: missing amount in remote payment")
	}
	valueStr, _ := amountRaw["value"].(string)
	currency, _ := amountRaw["currency"].(string)

	remoteKopecks, err := rublesStringToKopecks(valueStr)
	if err != nil {
		return false, fmt.Errorf("webhook: invalid remote amount %q", valueStr)
	}
	if currency != local.Currency {
		return false, nil
	}
	// Точное целочисленное сравнение копеек.
	return remoteKopecks == local.AmountKopecks, nil
}

// ListByUser возвращает платежи пользователя.
func (s *PaymentService) ListByUser(ctx context.Context, userID uint, limit int) ([]Payment, error) {
	return s.repo.ListByUser(ctx, userID, limit)
}
