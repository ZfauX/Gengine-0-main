// internal/domain/user/two_factor_service_test.go
package user

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTwoFactorService_GenerateSecret(t *testing.T) {
	svc := NewTwoFactorService()

	secret1, err := svc.GenerateSecret()
	require.NoError(t, err)
	assert.NotEmpty(t, secret1)
	assert.Len(t, secret1, 32)

	secret2, err := svc.GenerateSecret()
	require.NoError(t, err)
	assert.NotEqual(t, secret1, secret2)
}

func TestTwoFactorService_GenerateQRCodeURL(t *testing.T) {
	svc := NewTwoFactorService()

	secret := "JBSWY3DPEHPK3PXP"
	qrURL, err := svc.GenerateQRCodeURL(secret, "test@example.com", "Gengine-0")
	require.NoError(t, err)
	assert.Contains(t, qrURL, "otpauth://totp")
	assert.Contains(t, qrURL, "Gengine-0")
	assert.Contains(t, qrURL, "test@example.com")
}

func TestTwoFactorService_VerifyCode_Valid(t *testing.T) {
	svc := NewTwoFactorService()

	secret, err := svc.GenerateSecret()
	require.NoError(t, err)

	code, err := svc.GenerateTOTPCode(secret)
	require.NoError(t, err)

	valid, err := svc.VerifyCode(secret, code)
	require.NoError(t, err)
	assert.True(t, valid)
}

func TestTwoFactorService_VerifyCode_Invalid(t *testing.T) {
	svc := NewTwoFactorService()

	secret, err := svc.GenerateSecret()
	require.NoError(t, err)

	valid, err := svc.VerifyCode(secret, "000000")
	require.NoError(t, err)
	assert.False(t, valid)
}

func TestTwoFactorService_GenerateBackupCodes(t *testing.T) {
	svc := NewTwoFactorService()

	codes, err := svc.GenerateBackupCodes()
	require.NoError(t, err)
	assert.Len(t, codes, 10)

	// Все коды должны быть 6-значными
	for _, code := range codes {
		assert.Len(t, code, 6)
		// Проверяем, что код содержит только цифры
		assert.Regexp(t, `^\d{6}$`, code)
	}
}

func TestTwoFactorService_HashAndVerifyBackupCodes(t *testing.T) {
	svc := NewTwoFactorService()

	codes, err := svc.GenerateBackupCodes()
	require.NoError(t, err)

	hashed, err := svc.HashBackupCodes(codes)
	require.NoError(t, err)
	assert.NotEmpty(t, hashed)

	// Проверяем, что первый код проходит верификацию
	valid, err := svc.VerifyBackupCode(hashed, codes[0])
	require.NoError(t, err)
	assert.True(t, valid)

	// Проверяем, что второй код тоже проходит
	valid, err = svc.VerifyBackupCode(hashed, codes[1])
	require.NoError(t, err)
	assert.True(t, valid)

	// Проверяем, что неверный код не проходит
	valid, err = svc.VerifyBackupCode(hashed, "999999")
	require.NoError(t, err)
	assert.False(t, valid)
}

func TestTwoFactorService_ParseBackupCodeFromString(t *testing.T) {
	svc := NewTwoFactorService()

	codes := svc.ParseBackupCodeFromString("")
	assert.Empty(t, codes)

	codes = svc.ParseBackupCodeFromString("code1,,code2,  ,code3")
	assert.Len(t, codes, 3)
	assert.Equal(t, "code1", codes[0])
	assert.Equal(t, "code2", codes[1])
	assert.Equal(t, "code3", codes[2])
}

func TestTwoFactorService_Enable2FA(t *testing.T) {
	svc := NewTwoFactorService()

	user := &User{
		Model: gorm.Model{ID: 1},
		Email: "test@example.com",
	}

	err := svc.Enable2FA(user)
	require.NoError(t, err)

	assert.True(t, user.TwoFactorEnabled)
	assert.NotEmpty(t, user.TwoFactorSecret)
	assert.NotEmpty(t, user.TwoFactorBackupCodes)
	assert.Len(t, user.TwoFactorSecret, 32)
}

func TestTwoFactorService_Disable2FA(t *testing.T) {
	svc := NewTwoFactorService()

	user := &User{
		Model:                gorm.Model{ID: 1},
		Email:                "test@example.com",
		TwoFactorEnabled:     true,
		TwoFactorSecret:      "JBSWY3DPEHPK3PXP",
		TwoFactorBackupCodes: "hashed_codes",
	}

	svc.Disable2FA(user)

	assert.False(t, user.TwoFactorEnabled)
	assert.Empty(t, user.TwoFactorSecret)
	assert.Empty(t, user.TwoFactorBackupCodes)
}

func TestTwoFactorService_Validate2FAInput(t *testing.T) {
	svc := NewTwoFactorService()

	tests := []struct {
		name    string
		code    string
		wantErr bool
	}{
		{"empty", "", true},
		{"short", "12345", true},
		{"long", "1234567", true},
		{"non_numeric", "abcdef", true},
		{"valid", "123456", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := svc.Validate2FAInput(tt.code)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestTwoFactorService_GetBackupCodesCount(t *testing.T) {
	svc := NewTwoFactorService()

	codes, _ := svc.GenerateBackupCodes()
	hashed, _ := svc.HashBackupCodes(codes)

	count := svc.GetBackupCodesCount(hashed)
	assert.Equal(t, 10, count)

	count = svc.GetBackupCodesCount("")
	assert.Equal(t, 0, count)
}
