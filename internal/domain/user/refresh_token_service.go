// internal/domain/user/refresh_token_service.go
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

	"github.com/rs/zerolog/log"
)

// AccessTokenGenerator генерирует access-токен JWT для пользователя.
// Реализуется AuthService — RefreshTokenService не знает деталей JWT.
type AccessTokenGenerator interface {
	GenerateJWT(user User) (string, error)
}

// RefreshTokenService отвечает за выпуск, ротацию и отзыв refresh-токенов.
// D2: выделен из AuthService (JWT+refresh+lockout+blacklist → RefreshTokenService),
// чтобы ответственность за refresh-сессии была изолирована и тестируема отдельно.
type RefreshTokenService struct {
	refreshTokenRepo RefreshTokenRepository
	userRepo         UserRepository
	cfg              *config.Config
	accessGen        AccessTokenGenerator
}

// NewRefreshTokenService создаёт сервис refresh-токенов.
func NewRefreshTokenService(
	refreshTokenRepo RefreshTokenRepository,
	userRepo UserRepository,
	cfg *config.Config,
	accessGen AccessTokenGenerator,
) *RefreshTokenService {
	return &RefreshTokenService{
		refreshTokenRepo: refreshTokenRepo,
		userRepo:         userRepo,
		cfg:              cfg,
		accessGen:        accessGen,
	}
}

// GenerateRefreshToken создаёт refresh-токен для новой сессии.
func (s *RefreshTokenService) GenerateRefreshToken(ctx context.Context, user User, deviceID, clientFingerprint string) (string, error) {
	return s.generateRefreshToken(ctx, user, deviceID, clientFingerprint, "")
}

// generateRefreshToken создаёт refresh-токен. Если familyID пуст — генерируется
// новая семья (новый вход). Иначе токен относится к той же семье (ротация).
func (s *RefreshTokenService) generateRefreshToken(ctx context.Context, user User, deviceID, clientFingerprint, familyID string) (string, error) {
	token, record, err := s.buildRefreshToken(user, deviceID, clientFingerprint, familyID)
	if err != nil {
		return "", err
	}
	if err := s.refreshTokenRepo.Create(ctx, record); err != nil {
		return "", err
	}
	return token, nil
}

// buildRefreshToken формирует refresh-токен и его запись без сохранения
// (позволяет атомарный ClaimAndCreate при ротации, C-2).
func (s *RefreshTokenService) buildRefreshToken(user User, deviceID, clientFingerprint, familyID string) (string, *RefreshToken, error) {
	b := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	token := hex.EncodeToString(b)
	hash := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(hash[:])

	if familyID == "" {
		fam := make([]byte, 16)
		if _, err := rand.Read(fam); err != nil {
			return "", nil, err
		}
		familyID = hex.EncodeToString(fam)
	}

	record := &RefreshToken{
		UserID:            user.ID,
		TokenHash:         tokenHash,
		FamilyID:          familyID,
		DeviceID:          deviceID,
		ClientFingerprint: clientFingerprint,
		ExpiresAt:         time.Now().Add(s.cfg.JWT.RefreshExpiry),
	}
	return token, record, nil
}

// RevokeAllUserTokens отзывает все refresh-токены пользователя.
func (s *RefreshTokenService) RevokeAllUserTokens(ctx context.Context, userID uint) error {
	return s.refreshTokenRepo.RevokeAllForUser(ctx, userID)
}

// RevokeRefreshToken отзывает конкретный refresh-токен.
func (s *RefreshTokenService) RevokeRefreshToken(ctx context.Context, refreshTokenStr string) error {
	hash := sha256.Sum256([]byte(refreshTokenStr))
	tokenHash := hex.EncodeToString(hash[:])

	stored, err := s.refreshTokenRepo.GetByTokenHash(ctx, tokenHash)
	if err != nil {
		return err
	}
	return s.refreshTokenRepo.Revoke(ctx, stored.ID)
}

// CleanExpiredRefreshTokens удаляет просроченные refresh-токены.
func (s *RefreshTokenService) CleanExpiredRefreshTokens(ctx context.Context) error {
	return s.refreshTokenRepo.DeleteExpired(ctx)
}

// RefreshAccessToken проверяет refresh-токен и выдаёт новую пару access+refresh.
func (s *RefreshTokenService) RefreshAccessToken(ctx context.Context, refreshTokenStr, deviceID, clientFingerprint string) (string, string, error) {
	hash := sha256.Sum256([]byte(refreshTokenStr))
	tokenHash := hex.EncodeToString(hash[:])

	stored, err := s.refreshTokenRepo.GetByTokenHash(ctx, tokenHash)
	if err != nil {
		// Токен не найден среди активных. Если он существует, но уже отозван —
		// это reuse (повторное использование) отозванного токена = признак кражи.
		revoked, rErr := s.refreshTokenRepo.GetByTokenHashIncludingRevoked(ctx, tokenHash)
		if rErr == nil && revoked != nil && revoked.RevokedAt != nil && revoked.FamilyID != "" {
			if famErr := s.refreshTokenRepo.RevokeAllByFamily(ctx, revoked.FamilyID); famErr != nil {
				log.Error().Err(famErr).Uint("user_id", revoked.UserID).Str("family_id", revoked.FamilyID).Msg("RefreshAccessToken: family revoke failed")
			}
			log.Warn().Uint("user_id", revoked.UserID).Str("family_id", revoked.FamilyID).Msg("RefreshAccessToken: refresh token reuse detected — family revoked")
			return "", "", stderrors.New("refresh-токен уже использован — все сессии отозваны")
		}
		return "", "", stderrors.New("невалидный или отозванный refresh-токен")
	}
	if stored.ExpiresAt.Before(time.Now()) {
		return "", "", stderrors.New("refresh-токен истёк")
	}

	// Token binding: validate client fingerprint if stored.
	// Пустой fingerprint клиента НЕ обходит проверку (S5): если токен был
	// привязан к устройству, требуется точное совпадение.
	if stored.ClientFingerprint != "" && stored.ClientFingerprint != clientFingerprint {
		// #5: mismatch с другого устройства = признак кражи — отзываем семью,
		// как при reuse, и логируем.
		if stored.FamilyID != "" {
			if famErr := s.refreshTokenRepo.RevokeAllByFamily(ctx, stored.FamilyID); famErr != nil {
				log.Error().Err(famErr).Uint("user_id", stored.UserID).Str("family_id", stored.FamilyID).Msg("RefreshAccessToken: family revoke failed on fingerprint mismatch")
			}
		}
		log.Warn().Uint("user_id", stored.UserID).Str("family_id", stored.FamilyID).Msg("RefreshAccessToken: fingerprint mismatch — family revoked")
		return "", "", stderrors.New("отпечаток клиента не совпадает — используйте токен с того же устройства")
	}

	user, err := s.userRepo.GetByID(ctx, stored.UserID)
	if err != nil {
		return "", "", stderrors.New("пользователь не найден")
	}

	accessToken, err := s.accessGen.GenerateJWT(*user)
	if err != nil {
		return "", "", err
	}

	// Формируем новый refresh-токен (та же семья, та же привязка) и
	// атомарно отзываем старый + сохраняем новый в одной транзакции (C-2):
	// сбой создания не оставляет клиента без refresh-токена.
	newToken, newRecord, err := s.buildRefreshToken(*user, deviceID, stored.ClientFingerprint, stored.FamilyID)
	if err != nil {
		return "", "", err
	}
	claimed, claimErr := s.refreshTokenRepo.ClaimAndCreate(ctx, stored.ID, newRecord)
	if claimErr != nil {
		return "", "", fmt.Errorf("не удалось отозвать старый refresh-токен: %w", claimErr)
	}
	if !claimed {
		if stored.FamilyID != "" {
			if famErr := s.refreshTokenRepo.RevokeAllByFamily(ctx, stored.FamilyID); famErr != nil {
				log.Error().Err(famErr).Uint("user_id", stored.UserID).Str("family_id", stored.FamilyID).Msg("RefreshAccessToken: family revoke failed")
			}
		}
		log.Warn().Uint("user_id", stored.UserID).Str("family_id", stored.FamilyID).Msg("RefreshAccessToken: concurrent reuse detected — family revoked")
		return "", "", stderrors.New("refresh-токен уже использован — все сессии отозваны")
	}

	return accessToken, newToken, nil
}
