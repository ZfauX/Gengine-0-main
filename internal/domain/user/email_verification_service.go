// internal/domain/user/email_verification_service.go
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
	"gengine-0/internal/pkg/email"
	errspkg "gengine-0/internal/pkg/errors"

	"github.com/rs/zerolog/log"
)

// ---------- EmailVerificationService ----------

type EmailVerificationService struct {
	userRepo       UserRepository
	emailVerifRepo EmailVerificationRepository
	cfg            *config.Config
}

func NewEmailVerificationService(
	userRepo UserRepository,
	emailVerifRepo EmailVerificationRepository,
	cfg *config.Config,
) *EmailVerificationService {
	return &EmailVerificationService{
		userRepo:       userRepo,
		emailVerifRepo: emailVerifRepo,
		cfg:            cfg,
	}
}

func (s *EmailVerificationService) SendVerificationEmail(ctx context.Context, user User) error {
	// Если SMTP отключён, токен не создаём — верификация не работает без почты
	if !s.cfg.SMTP.Enabled {
		return nil
	}

	// Удаляем предыдущий токен, если есть (теперь UserID не uniqueIndex)
	errspkg.LogSilently(s.emailVerifRepo.DeleteByUserID(ctx, user.ID), "SendVerificationEmail: old token cleanup")

	b := make([]byte, emailVerificationTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return fmt.Errorf("не удалось сгенерировать токен верификации: %w", err)
	}
	token := hex.EncodeToString(b)
	hash := sha256.Sum256([]byte(token))

	// Короткий одноразовый код (8 символов) для ссылки — без токена в URL
	codeBytes := make([]byte, 6)
	if _, err := rand.Read(codeBytes); err != nil {
		return fmt.Errorf("не удалось сгенерировать код верификации: %w", err)
	}
	verificationCode := hex.EncodeToString(codeBytes)

	if err := s.emailVerifRepo.CreateToken(ctx, &EmailVerificationToken{
		UserID:           user.ID,
		TokenHash:        hex.EncodeToString(hash[:]),
		VerificationCode: verificationCode,
		ExpiresAt:        time.Now().Add(emailVerificationExpiry),
	}); err != nil {
		return fmt.Errorf("не удалось сохранить токен верификации: %w", err)
	}
	if err := email.Enqueue(
		user.Email,
		"Подтверждение email",
		fmt.Sprintf("Код подтверждения: %s\n\nПерейдите по ссылке: %s/auth/verify?code=%s",
			verificationCode, s.cfg.Server.BaseURL, verificationCode),
	); err != nil {
		log.Error().Err(err).Str("email", user.Email).Msg("SendVerificationEmail: failed to enqueue email")
		// Удаляем токен по хешу, так как письмо не ушло
		tokenHash := hex.EncodeToString(hash[:])
		errspkg.LogSilently(s.emailVerifRepo.DeleteByTokenHash(ctx, tokenHash), "SendVerificationEmail: cleanup failed")
		return fmt.Errorf("не удалось отправить письмо: %w", err)
	}
	return nil
}

func (s *EmailVerificationService) VerifyByCode(ctx context.Context, code string) (*User, error) {
	token, err := s.emailVerifRepo.GetTokenByCode(ctx, code)
	if err != nil {
		return nil, stderrors.New("код недействителен или истёк")
	}
	if token.ExpiresAt.Before(time.Now()) {
		return nil, stderrors.New("код истёк")
	}
	if err := s.userRepo.Update(ctx, token.UserID, map[string]any{"email_verified": true}); err != nil {
		return nil, err
	}
	errspkg.LogSilently(s.emailVerifRepo.DeleteToken(ctx, token), "VerifyByCode: cleanup failed")
	return s.userRepo.GetByID(ctx, token.UserID)
}
