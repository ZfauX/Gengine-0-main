// internal/pkg/security/password_breach.go
package security

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	haveIBeenPwnedURL = "https://api.pwnedpasswords.com/range/"
	httpTimeout       = 10 * time.Second
)

var httpClient = &http.Client{
	Timeout: httpTimeout,
}

// CheckPasswordBreach проверяет, не был ли пароль взятом в утечке данных.
// Возвращает true, если пароль НЕ взломан (безопасен), false если найден.
func CheckPasswordBreach(password string) (safe bool, count int, err error) {
	if password == "" {
		return true, 0, nil
	}

	hash := sha1.Sum([]byte(password))
	hashStr := hex.EncodeToString(hash[:])
	prefix := hashStr[:5]
	suffix := hashStr[5:]

	resp, err := httpClient.Get(haveIBeenPwnedURL + prefix)
	if err != nil {
		return false, 0, fmt.Errorf("failed to check password breach: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return false, 0, fmt.Errorf("rate limited by HaveIBeenPwned API")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, 0, fmt.Errorf("failed to read response: %w", err)
	}

	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(strings.ToUpper(line), suffix) {
			parts := strings.Split(line, ":")
			if len(parts) == 2 {
				if _, err := fmt.Sscanf(parts[1], "%d", &count); err != nil {
					continue
				}
				return false, count, nil
			}
		}
	}

	return true, 0, nil
}

// ValidatePasswordWithBreachCheck проверяет пароль на соответствие требованиям и проверяет утечки.
func ValidatePasswordWithBreachCheck(password string) error {
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	if len(password) > 128 {
		return fmt.Errorf("password too long (max 128 characters)")
	}

	safe, count, err := CheckPasswordBreach(password)
	if err != nil {
		return fmt.Errorf("password validation error: %w", err)
	}
	if !safe {
		return fmt.Errorf("password found in %d data breaches, please choose another", count)
	}

	return nil
}
