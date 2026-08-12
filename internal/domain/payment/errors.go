// internal/domain/payment/errors.go
// S-46-3 (pass 46): классификация ошибок вебхука ЮKassa.
// Позволяет хендлеру вернуть корректный HTTP-статус:
//   - ErrWebhookInvalid — 400: тело/платёж некорректен (ретраить бессмысленно);
//   - ErrWebhookUntrustedIP — 403: запрос не с IP ЮKassa;
//   - ErrWebhookUnauthorized — 401: неверный Authorization (подпись вебхука, M1);
//   - прочие ошибки — 500: временные (ЮKassa/БД), ЮKassa будет ретраить.
package payment

import "errors"

var (
	// ErrWebhookInvalid — некорректное тело уведомления или отсутствует payment id.
	ErrWebhookInvalid = errors.New("webhook: invalid body")
	// ErrWebhookUntrustedIP — запрос пришёл не с IP-адресов ЮKassa.
	ErrWebhookUntrustedIP = errors.New("webhook from untrusted IP")
	// ErrWebhookUnauthorized — неверный Authorization (DEEP-REVIEW PASS-3 M1).
	ErrWebhookUnauthorized = errors.New("webhook: unauthorized")
	// ErrWebhookEventMismatch — event из тела не совпадает со статусом из API
	// (L3, PASS-5) — подделка/устаревшее уведомление, 4xx.
	ErrWebhookEventMismatch = errors.New("webhook: event mismatch with API status")
	// ErrWebhookAmountMismatch — сумма/валюта из API не совпадает с локальной
	// (L3, PASS-5) — 4xx, ЮKassa перестаёт ретраить.
	ErrWebhookAmountMismatch = errors.New("webhook: amount mismatch")
)
