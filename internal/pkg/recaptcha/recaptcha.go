// Package recaptcha — серверная проверка токена Google reCAPTCHA (S-3, pass 35).
// Ранее конфиг reCAPTCHA загружался, но нигде не использовался: виджет в
// auth-register.html рендерился с пустым site-key, а токен не проверялся —
// «мёртвая» защита от ботов.
package recaptcha

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// verifyURL — эндпоинт siteverify Google. var (не const) для тестов
// (recaptcha_test.go подменяет на httptest.Server).
var verifyURL = "https://www.google.com/recaptcha/api/siteverify"

// siteverifyResponse — ответ Google API.
type siteverifyResponse struct {
	Success    bool     `json:"success"`
	ErrorCodes []string `json:"error-codes"`
}

// Client проверяет токены reCAPTCHA против секретного ключа.
type Client struct {
	secretKey string
	http      *http.Client
	enabled   bool
}

// NewClient создаёт клиент проверки reCAPTCHA. Если enabled == false или
// secretKey пустой — Verify всегда возвращает true (защита выключена).
func NewClient(enabled bool, secretKey string) *Client {
	return &Client{
		enabled:   enabled,
		secretKey: secretKey,
		http: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// Enabled сообщает, включена ли проверка.
func (c *Client) Enabled() bool {
	return c != nil && c.enabled && c.secretKey != ""
}

// Verify проверяет токен reCAPTCHA. Возвращает nil при успехе.
// Если защита отключена — всегда nil (пропускает).
func (c *Client) Verify(ctx context.Context, token string) error {
	if !c.Enabled() {
		return nil
	}
	if token == "" {
		return fmt.Errorf("recaptcha: empty token")
	}

	form := url.Values{}
	form.Set("secret", c.secretKey)
	form.Set("response", token)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, verifyURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("recaptcha: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("recaptcha: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("recaptcha: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		log.Warn().Int("status", resp.StatusCode).Msg("recaptcha: unexpected status")
		return fmt.Errorf("recaptcha: unexpected status %d", resp.StatusCode)
	}

	var result siteverifyResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("recaptcha: decode response: %w", err)
	}
	if !result.Success {
		log.Warn().Strs("error_codes", result.ErrorCodes).Msg("recaptcha: verification failed")
		return fmt.Errorf("recaptcha: verification failed (%v)", result.ErrorCodes)
	}
	return nil
}
