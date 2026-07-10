// internal/domain/user/two_factor_service.go
package user

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/bcrypt"
)

// TwoFactorService отвечает за управление двухфакторной аутентификацией.
type TwoFactorService struct{}

// NewTwoFactorService создаёт новый сервис 2FA.
func NewTwoFactorService() *TwoFactorService {
	return &TwoFactorService{}
}

// GenerateSecret генерирует новый случайный секрет для TOTP.
func (s *TwoFactorService) GenerateSecret() (string, error) {
	secret := make([]byte, 20)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("ошибка генерации секрета: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret), nil
}

// GenerateQRCodeURL создаёт URL для QR-кода Google Authenticator.
func (s *TwoFactorService) GenerateQRCodeURL(secret, email, appName string) (string, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      appName,
		AccountName: email,
		Secret:      []byte(secret),
	})
	if err != nil {
		return "", fmt.Errorf("ошибка генерации QR-кода: %w", err)
	}
	return key.URL(), nil
}

// VerifyCode проверяет TOTP-код.
func (s *TwoFactorService) VerifyCode(secret, code string) (bool, error) {
	// Убираем пробелы из кода
	code = strings.ReplaceAll(code, " ", "")

	valid := totp.Validate(code, secret)

	return valid, nil
}

// GenerateTOTPCode генерирует валидный TOTP-код для тестирования.
func (s *TwoFactorService) GenerateTOTPCode(secret string) (string, error) {
	return totp.GenerateCode(secret, time.Now())
}

// GenerateBackupCodes генерирует 10 резервных кодов для восстановления доступа.
func (s *TwoFactorService) GenerateBackupCodes() ([]string, error) {
	codes := make([]string, 10)
	for i := 0; i < 10; i++ {
		bytes := make([]byte, 4)
		if _, err := rand.Read(bytes); err != nil {
			return nil, fmt.Errorf("ошибка генерации резервного кода: %w", err)
		}
		hexStr := hex.EncodeToString(bytes)[:6]
		num, _ := strconv.Atoi(hexStr)
		code := fmt.Sprintf("%06d", num%1000000)
		codes[i] = code
	}
	return codes, nil
}

// HashBackupCodes хеширует резервные коды для хранения в БД.
func (s *TwoFactorService) HashBackupCodes(codes []string) (string, error) {
	hashed := make([]string, len(codes))
	for i, code := range codes {
		bytes, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
		if err != nil {
			return "", fmt.Errorf("ошибка хеширования резервного кода: %w", err)
		}
		hashed[i] = string(bytes)
	}
	return strings.Join(hashed, ","), nil
}

// VerifyBackupCode проверяет резервный код.
func (s *TwoFactorService) VerifyBackupCode(stored, code string) (bool, error) {
	codes := strings.Split(stored, ",")
	for _, hashed := range codes {
		hashed = strings.TrimSpace(hashed)
		if hashed == "" {
			continue
		}
		err := bcrypt.CompareHashAndPassword([]byte(hashed), []byte(code))
		if err == nil {
			return true, nil
		}
	}
	return false, nil
}

// ParseBackupCodeFromString преобразует строку с кодами в массив.
func (s *TwoFactorService) ParseBackupCodeFromString(stored string) []string {
	codes := strings.Split(stored, ",")
	result := make([]string, 0, len(codes))
	for _, code := range codes {
		code = strings.TrimSpace(code)
		if code != "" {
			result = append(result, code)
		}
	}
	return result
}

// Enable2FA включает двухфакторную аутентификацию для пользователя.
func (s *TwoFactorService) Enable2FA(user *User) error {
	secret, err := s.GenerateSecret()
	if err != nil {
		return err
	}

	backupCodes, err := s.GenerateBackupCodes()
	if err != nil {
		return err
	}

	hashedCodes, err := s.HashBackupCodes(backupCodes)
	if err != nil {
		return err
	}

	user.TwoFactorEnabled = true
	user.TwoFactorSecret = secret
	user.TwoFactorBackupCodes = hashedCodes

	log.Info().
		Str("user_id", fmt.Sprintf("%d", user.ID)).
		Str("email", user.Email).
		Msg("2FA enabled for user")

	return nil
}

// Disable2FA отключает двухфакторную аутентификацию.
func (s *TwoFactorService) Disable2FA(user *User) {
	user.TwoFactorEnabled = false
	user.TwoFactorSecret = ""
	user.TwoFactorBackupCodes = ""

	log.Info().
		Str("user_id", fmt.Sprintf("%d", user.ID)).
		Str("email", user.Email).
		Msg("2FA disabled for user")
}

// Validate2FAInput проверяет валидность входных данных для 2FA.
func (s *TwoFactorService) Validate2FAInput(code string) error {
	if code == "" {
		return fmt.Errorf("введите код подтверждения")
	}
	if len(code) != 6 {
		return fmt.Errorf("код должен содержать 6 цифр")
	}
	if _, err := strconv.Atoi(code); err != nil {
		return fmt.Errorf("код должен содержать только цифры")
	}
	return nil
}

// GetBackupCodesCount возвращает количество активных резервных кодов.
func (s *TwoFactorService) GetBackupCodesCount(stored string) int {
	codes := s.ParseBackupCodeFromString(stored)
	return len(codes)
}
