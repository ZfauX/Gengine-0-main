// internal/domain/user/password_reset_service.go
package user

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	stderrors "errors"
	"fmt"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/pkg/crypto"
	"gengine-0/internal/pkg/email"

	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// ---------- PasswordResetService ----------

type PasswordResetService struct {
	userRepo      UserRepository
	passResetRepo PasswordResetRepository
	cfg           *config.Config
}

func NewPasswordResetService(
	userRepo UserRepository,
	passResetRepo PasswordResetRepository,
	cfg *config.Config,
) *PasswordResetService {
	return &PasswordResetService{
		userRepo:      userRepo,
		passResetRepo: passResetRepo,
		cfg:           cfg,
	}
}

func (s *PasswordResetService) GenerateToken(ctx context.Context, user User) (string, error) {
	b := make([]byte, passwordResetTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("не удалось сгенерировать токен: %w", err)
	}
	rawToken := hex.EncodeToString(b)
	hash := sha256.Sum256([]byte(rawToken))

	codeBytes := make([]byte, 16)
	if _, err := rand.Read(codeBytes); err != nil {
		return "", fmt.Errorf("не удалось сгенерировать код сброса: %w", err)
	}
	resetCode := hex.EncodeToString(codeBytes)

	token := PasswordResetToken{
		UserID:    user.ID,
		ResetCode: resetCode,
		TokenHash: hex.EncodeToString(hash[:]),
		ExpiresAt: time.Now().Add(passwordResetExpiry),
	}
	if err := s.passResetRepo.CreateToken(ctx, &token); err != nil {
		return "", err
	}
	if s.cfg.SMTP.Enabled {
		if err := email.Enqueue(
			user.Email,
			"Сброс пароля",
			fmt.Sprintf("Для сброса пароля перейдите по ссылке: %s/auth/reset/%s", s.cfg.Server.BaseURL, resetCode),
		); err != nil {
			log.Error().Err(err).Str("email", user.Email).Msg("failed to enqueue password reset email")
		}
	}
	return resetCode, nil
}

// GetUserIDByResetCode возвращает ID пользователя по коду сброса (без валидации — только для логирования).
func (s *PasswordResetService) GetUserIDByResetCode(ctx context.Context, resetCode string) uint {
	token, err := s.passResetRepo.GetTokenByResetCode(ctx, resetCode)
	if err != nil {
		return 0
	}
	return token.UserID
}

func (s *PasswordResetService) ResetPassword(ctx context.Context, resetCode, newPassword string) error {
	token, err := s.passResetRepo.GetTokenByResetCode(ctx, resetCode)
	if err != nil {
		return stderrors.New("токен недействителен или истёк")
	}
	if token.ExpiresAt.Before(time.Now()) {
		return stderrors.New("токен истёк")
	}
	if token.UsedAt != nil {
		return stderrors.New("токен уже использован")
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(newPassword), crypto.BcryptCost)
	if err != nil {
		return err
	}
	now := time.Now()

	// Сначала потребляем токен (атомарно, WHERE used_at IS NULL) — при сбое
	// между шагами токен уже мёртв, а не остаётся живым после смены пароля (B5).
	if err := s.passResetRepo.MarkTokenUsed(ctx, token.ID, now); err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return stderrors.New("токен уже использован")
		}
		return err
	}
	if err := s.userRepo.Update(ctx, token.UserID, map[string]any{"password": string(hashed)}); err != nil {
		return err
	}
	return s.passResetRepo.DeleteToken(ctx, token)
}
