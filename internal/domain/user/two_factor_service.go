// internal/domain/user/two_factor_service.go
package user

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/rs/zerolog/log"
	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/crypto/bcrypt"
)

const (
	totpSecretBytes = 20
	backupCodeCount = 10
	totpCodeLength  = 6
	// backupCodeLength — длина резервного кода (S-2, pass 31).
	backupCodeLength = 10
)

// backupCodeAlphabet — символы без неоднозначных (0/O, 1/I, l).
const backupCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// normalizeBackupCode приводит введённый код к каноническому виду (S-3, pass 32):
// верхний регистр + удаление дефисов/пробелов — пользователь может ввести
// код с тире (ABCD-EFGH) или в нижнем регистре.
func normalizeBackupCode(code string) string {
	code = strings.ToUpper(code)
	return strings.NewReplacer("-", "", " ", "", "_", "").Replace(code)
}

// TwoFactorService отвечает за управление двухфакторной аутентификацией.
type TwoFactorService struct{}

// NewTwoFactorService создаёт новый сервис 2FA.
func NewTwoFactorService() *TwoFactorService {
	return &TwoFactorService{}
}

// GenerateSecret генерирует новый случайный секрет для TOTP.
func (s *TwoFactorService) GenerateSecret() (string, error) {
	secret := make([]byte, totpSecretBytes)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("ошибка генерации секрета: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret), nil
}

// GenerateQRCodeURL создаёт URL для QR-кода Google Authenticator.
// ВНИМАНИЕ (pass 29 / CRIT-1): URL содержит сам TOTP-секрет — его нельзя
// передавать сторонним сервисам (например, api.qrserver.com). Используйте
// GenerateQRCodePNG для локальной генерации QR-картинки.
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

// GenerateQRCodePNG генерирует PNG-картинку QR-кода локально (CRIT-1).
// Секрет никуда не уходит: картинка создаётся на сервере и отдаётся
// только самому пользователю.
func (s *TwoFactorService) GenerateQRCodePNG(secret, email, appName string, size int) ([]byte, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      appName,
		AccountName: email,
		Secret:      []byte(secret),
	})
	if err != nil {
		return nil, fmt.Errorf("ошибка генерации QR-кода: %w", err)
	}
	if size <= 0 {
		size = 200
	}
	code, err := qrcode.New(key.URL(), qrcode.Medium)
	if err != nil {
		return nil, fmt.Errorf("ошибка создания QR-кода: %w", err)
	}
	return code.PNG(size)
}

// VerifyCode проверяет TOTP-код.
func (s *TwoFactorService) VerifyCode(secret, code string) (bool, error) {
	// Убираем пробелы из кода
	code = strings.ReplaceAll(code, " ", "")

	// Validate code format — must be exactly 6 digits
	if len(code) != 6 {
		return false, nil
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return false, nil
		}
	}

	valid := totp.Validate(code, secret)
	return valid, nil
}

// GenerateTOTPCode генерирует валидный TOTP-код для тестирования.
func (s *TwoFactorService) GenerateTOTPCode(secret string) (string, error) {
	return totp.GenerateCode(secret, time.Now())
}

// GenerateBackupCodes генерирует 10 резервных кодов для восстановления доступа.
// S-2 (pass 31): формат — 10 символов из алфавита без неоднозначных символов
// (0/O/1/I/L) → ~50 бит энтропии на код вместо ~20 бит у 6-значных цифр.
// 8 байт → uint64 → mod len(alphabet): равномерно и без hex-обрезания (M2).
func (s *TwoFactorService) GenerateBackupCodes() ([]string, error) {
	codes := make([]string, backupCodeCount)
	for i := 0; i < backupCodeCount; i++ {
		code := make([]byte, backupCodeLength)
		for j := 0; j < backupCodeLength; j++ {
			bytes := make([]byte, 8)
			if _, err := rand.Read(bytes); err != nil {
				return nil, fmt.Errorf("ошибка генерации резервного кода: %w", err)
			}
			num := binary.BigEndian.Uint64(bytes)
			code[j] = backupCodeAlphabet[num%uint64(len(backupCodeAlphabet))]
		}
		codes[i] = string(code)
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

// VerifyBackupCode проверяет резервный код. Ввод нормализуется (S-3, pass 32):
// коды генерируются в верхнем регистре, а пользователь может ввести нижний,
// с дефисами/пробелами.
func (s *TwoFactorService) VerifyBackupCode(stored, code string) (bool, error) {
	normalized := normalizeBackupCode(code)
	codes := strings.Split(stored, ",")
	for _, hashed := range codes {
		hashed = strings.TrimSpace(hashed)
		if hashed == "" {
			continue
		}
		err := bcrypt.CompareHashAndPassword([]byte(hashed), []byte(normalized))
		if err == nil {
			return true, nil
		}
	}
	return false, nil
}

// VerifyAndRemoveBackupCode проверяет резервный код и удаляет его из списка.
func (s *TwoFactorService) VerifyAndRemoveBackupCode(stored, code string) (string, error) {
	normalized := normalizeBackupCode(code)
	codes := strings.Split(stored, ",")
	for i, hashed := range codes {
		hashed = strings.TrimSpace(hashed)
		if hashed == "" {
			continue
		}
		err := bcrypt.CompareHashAndPassword([]byte(hashed), []byte(normalized))
		if err == nil {
			// Remove this code
			remaining := append(codes[:i], codes[i+1:]...)
			return strings.Join(remaining, ","), nil
		}
	}
	return stored, fmt.Errorf("неверный резервный код")
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
		Msg("2FA disabled for user")
}

// Validate2FAInput проверяет валидность входных данных для 2FA.
func (s *TwoFactorService) Validate2FAInput(code string) error {
	if code == "" {
		return fmt.Errorf("введите код подтверждения")
	}
	if len(code) != totpCodeLength {
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
