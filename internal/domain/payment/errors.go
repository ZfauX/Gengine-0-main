// internal/domain/payment/errors.go
// S-46-3 (pass 46): классификация ошибок вебхука ЮKassa.
// Позволяет хендлеру вернуть корректный HTTP-статус:
//   - ErrWebhookInvalid — 400: тело/платёж некорректен (ретраить бессмысленно);
//   - ErrWebhookUntrustedIP — 403: запрос не с IP ЮKassa;
//   - прочие ошибки — 500: временные (ЮKassa/БД), ЮKassa будет ретраить.
package payment

import "errors"

var (
	// ErrWebhookInvalid — некорректное тело уведомления или отсутствует payment id.
	ErrWebhookInvalid = errors.New("webhook: invalid body")
	// ErrWebhookUntrustedIP — запрос пришёл не с IP-адресов ЮKassa.
	ErrWebhookUntrustedIP = errors.New("webhook from untrusted IP")
)
