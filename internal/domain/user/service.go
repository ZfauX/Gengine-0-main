// internal/domain/user/service.go
//
// Note: tests use hand-rolled mocks or real DB; generated file may be incomplete
//
//go:generate go run go.uber.org/mock/mockgen -source=repository.go -destination=mock_service.go -package=user
package user

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/pkg/cache"
	"gengine-0/internal/pkg/crypto"
	"gengine-0/internal/pkg/email"
	errspkg "gengine-0/internal/pkg/errors"
	"gengine-0/internal/pkg/metrics"

	"github.com/golang-jwt/jwt/v5"
	"github.com/lib/pq"
	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/yandex"
	"gorm.io/gorm"
)

// dummyPasswordHash — bcrypt-хэш случайного пароля, генерируется при старте с тем же
// cost (12), что и реальные пароли. Используется для выравнивания времени ответа
// при попытке входа по несуществующему email (анти-энумерация).
var dummyPasswordHash = func() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("dummy-password-for-timing-"+randText()), crypto.BcryptCost)
	if err != nil {
		return []byte("$2a$12$invalid")
	}
	return h
}()

// randText возвращает случайную hex-строку (для инициализации dummy-хэша).
func randText() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "fallback"
	}
	return hex.EncodeToString(b)
}

const (
	refreshTokenBytes           = 32
	oauthStateBytes             = 16
	passwordResetTokenBytes     = 16
	emailVerificationTokenBytes = 16
	oauthHTTPTimeout            = 15 * time.Second
	passwordResetExpiry         = 1 * time.Hour
	emailVerificationExpiry     = 24 * time.Hour
)

// ---------- AuthService ----------

type AuthService struct {
	userRepo         UserRepository
	achievRepo       AchievementRepository
	emailVerifRepo   EmailVerificationRepository
	refreshTokenRepo RefreshTokenRepository
	cfg              *config.Config
	cache            cache.CacheStore
}

func NewAuthService(
	userRepo UserRepository,
	achievRepo AchievementRepository,
	emailVerifRepo EmailVerificationRepository,
	refreshTokenRepo RefreshTokenRepository,
	cfg *config.Config,
) *AuthService {
	return &AuthService{
		userRepo:         userRepo,
		achievRepo:       achievRepo,
		emailVerifRepo:   emailVerifRepo,
		refreshTokenRepo: refreshTokenRepo,
		cfg:              cfg,
	}
}

// WithCache sets the cache store for JWT blacklist support.
func (s *AuthService) WithCache(c cache.CacheStore) *AuthService {
	s.cache = c
	return s
}

func (s *AuthService) Register(ctx context.Context, emailStr, password, name string) (*User, error) {
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), crypto.BcryptCost)
	if err != nil {
		return nil, err
	}
	user := User{
		Email:    emailStr,
		Password: string(hashed),
		Name:     name,
	}
	if err := s.userRepo.Create(ctx, &user); err != nil {
		return nil, err
	}
	metrics.IncUsersTotal()

	verificationService := NewEmailVerificationService(s.userRepo, s.emailVerifRepo, s.cfg)
	if err := verificationService.SendVerificationEmail(ctx, user); err != nil {
		log.Warn().Err(err).Str("email", user.Email).Msg("Register: failed to send verification email")
	}

	return &user, nil
}

func (s *AuthService) Login(ctx context.Context, emailStr, password string) (string, error) {
	user, err := s.userRepo.GetByEmail(ctx, emailStr)
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			// Dummy bcrypt to prevent timing-based email enumeration.
			// Хэш генерируется с тем же BcryptCost (12), что и реальные пароли,
			// иначе тайминг-атака различает «нет пользователя» и «неверный пароль» (S3).
			bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(password)) //nolint:errcheck
		}
		return "", stderrors.New("неверный email или пароль")
	}

	// Проверка блокировки аккаунта.
	// Возвращаем ТОТ ЖЕ generic-ответ, что и для неверного пароля/несуществующего
	// email — иначе по сообщению «аккаунт заблокирован» атакующий узнаёт о
	// существовании аккаунта (oracle) (B2). Реальная причина логируется для ops.
	if user.LockedUntil != nil && time.Now().Before(*user.LockedUntil) {
		log.Debug().Uint("user_id", user.ID).Time("locked_until", *user.LockedUntil).Msg("Login: account is locked")
		return "", stderrors.New("неверный email или пароль")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		// Атомарный инкремент счётчика неудачных попыток (без race condition)
		newAttempts, err := s.userRepo.AtomicIncrementFailedAttempts(ctx, user.ID)
		if err != nil {
			log.Error().Err(err).Uint("user_id", user.ID).Msg("Login: atomic increment failed")
			return "", stderrors.New("внутренняя ошибка сервера")
		}

		if newAttempts >= 5 {
			now := time.Now()
			lockedUntil := now.Add(30 * time.Minute)
			if err := s.userRepo.Update(ctx, user.ID, map[string]any{
				"locked_until":          lockedUntil,
				"failed_login_attempts": 0,
			}); err != nil {
				log.Error().Err(err).Uint("user_id", user.ID).Msg("Login: failed to lock account")
				return "", stderrors.New("внутренняя ошибка сервера")
			}
			// Generic-ответ: не раскрываем существование аккаунта (B2).
			return "", stderrors.New("неверный email или пароль")
		}
		return "", stderrors.New("неверный email или пароль")
	}

	// Успешный вход — безусловный сброс счётчика
	if err := s.userRepo.Update(ctx, user.ID, map[string]any{"failed_login_attempts": 0, "locked_until": nil}); err != nil {
		log.Error().Err(err).Uint("user_id", user.ID).Msg("Login: failed to reset failed_login_attempts")
	}

	return s.generateJWT(*user)
}

func (s *AuthService) GenerateJWT(user User) (string, error) {
	return s.generateJWT(user)
}

func (s *AuthService) GenerateRefreshToken(ctx context.Context, user User, deviceID, clientFingerprint string) (string, error) {
	return s.generateRefreshToken(ctx, user, deviceID, clientFingerprint, "")
}

// generateRefreshToken создаёт refresh-токен. Если familyID пуст — генерируется
// новая семья (новый вход). Иначе токен относится к той же семье (ротация).
func (s *AuthService) generateRefreshToken(ctx context.Context, user User, deviceID, clientFingerprint, familyID string) (string, error) {
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
func (s *AuthService) buildRefreshToken(user User, deviceID, clientFingerprint, familyID string) (string, *RefreshToken, error) {
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

func (s *AuthService) RevokeAllUserTokens(ctx context.Context, userID uint) error {
	return s.refreshTokenRepo.RevokeAllForUser(ctx, userID)
}

func (s *AuthService) RevokeRefreshToken(ctx context.Context, refreshTokenStr string) error {
	hash := sha256.Sum256([]byte(refreshTokenStr))
	tokenHash := hex.EncodeToString(hash[:])

	stored, err := s.refreshTokenRepo.GetByTokenHash(ctx, tokenHash)
	if err != nil {
		return err
	}
	return s.refreshTokenRepo.Revoke(ctx, stored.ID)
}

func (s *AuthService) CleanExpiredRefreshTokens(ctx context.Context) error {
	return s.refreshTokenRepo.DeleteExpired(ctx)
}

func (s *AuthService) RefreshAccessToken(ctx context.Context, refreshTokenStr, deviceID, clientFingerprint string) (string, string, error) {
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

	accessToken, err := s.generateJWT(*user)
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

func (s *AuthService) ParseToken(tokenStr string) (uint, string, error) {
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, stderrors.New("неверный метод подписи")
		}
		return []byte(s.cfg.JWT.Secret), nil
	})
	if err != nil || token == nil || !token.Valid {
		return 0, "", stderrors.New("невалидный токен")
	}

	// Проверяем, что токен не refresh-токен
	if isRefresh, ok := claims["refresh"].(bool); ok && isRefresh {
		return 0, "", stderrors.New("использование refresh-токена как access запрещено")
	}

	// Проверяем nbf (not before)
	if nbf, ok := claims["nbf"].(float64); ok {
		if time.Now().Unix() < int64(nbf) {
			return 0, "", stderrors.New("токен ещё не действителен")
		}
	}

	// Проверяем iat (issued at)
	if iat, ok := claims["iat"].(float64); ok {
		if time.Now().Unix() < int64(iat) {
			return 0, "", stderrors.New("неверная дата выдачи токена")
		}
	}

	// Проверяем issuer/audience — токены, выпущенные другим сервисом/окружением,
	// не принимаем даже при совпадении секрета (S1).
	expectedIssuer := s.cfg.Server.BaseURL
	if expectedIssuer == "" {
		expectedIssuer = "gengine"
	}
	if iss, ok := claims["iss"].(string); !ok || iss != expectedIssuer {
		return 0, "", stderrors.New("неверный issuer токена")
	}
	if aud, ok := claims["aud"].(string); !ok || aud != expectedIssuer {
		return 0, "", stderrors.New("неверный audience токена")
	}

	// JTI blacklist check: если токен был отозван (logout, password change), отклоняем его
	if s.cache != nil {
		if jti, ok := claims["jti"].(string); ok && jti != "" {
			if _, found := s.cache.Get("jti_blacklist:" + jti); found {
				return 0, "", stderrors.New("токен был отозван")
			}
		}
	}

	// Проверяем user_id
	userIDFloat, ok := claims["user_id"]
	if !ok {
		return 0, "", stderrors.New("отсутствует user_id в токене")
	}

	var userID uint
	switch v := userIDFloat.(type) {
	case float64:
		userID = uint(v)
	case json.Number:
		parsed, parseErr := v.Int64()
		if parseErr != nil {
			return 0, "", stderrors.New("невалидный формат user_id в токене")
		}
		userID = uint(parsed)
	default:
		return 0, "", stderrors.New("неверный тип user_id в токене")
	}

	if userID == 0 {
		return 0, "", stderrors.New("невалидный ID пользователя в токене")
	}

	role := "user"
	if roleVal, ok := claims["role"].(string); ok && roleVal != "" {
		role = roleVal
	}

	return userID, role, nil
}

// RevokeJWT добавляет JWT в blacklist по JTI, чтобы он не мог быть использован даже до истечения срока.
// Требует настроенного кэша (Valkey). Если кэш не настроен, операция логирует предупреждение.
func (s *AuthService) RevokeJWT(ctx context.Context, tokenStr string) {
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, stderrors.New("неверный метод подписи")
		}
		return []byte(s.cfg.JWT.Secret), nil
	})
	if err != nil || token == nil || !token.Valid {
		return
	}

	jti, ok := claims["jti"].(string)
	if !ok || jti == "" {
		return
	}

	// Extract remaining TTL for the blacklist entry
	expFloat, ok := claims["exp"].(float64)
	if !ok {
		return
	}
	ttl := time.Until(time.Unix(int64(expFloat), 0))
	if ttl <= 0 {
		return
	}

	if s.cache != nil {
		s.cache.SetWithCtx(ctx, "jti_blacklist:"+jti, true, ttl)
	} else {
		// Revocation is best-effort without a cache — log loudly so ops notices
		log.Warn().Str("jti", jti).Msg("RevokeJWT: no cache configured — JWT revocation is NOT enforced")
	}
}

func (s *AuthService) generateJWT(user User) (string, error) {
	// TODO: Implement JTI blacklist via Valkey for token revocation
	// Generate unique token ID for potential revocation (jti blacklist via Valkey)
	jti := make([]byte, 16)
	if _, err := rand.Read(jti); err != nil {
		return "", fmt.Errorf("jti generation failed: %w", err)
	}

	// Issuer/audience: защита от приёма токенов, выпущенных другим сервисом
	// или для другого окружения, подписанных тем же секретом (S1).
	issuer := s.cfg.Server.BaseURL
	if issuer == "" {
		issuer = "gengine"
	}

	now := time.Now()
	claims := jwt.MapClaims{
		"user_id": user.ID,
		"email":   user.Email,
		"role":    user.Role,
		"jti":     hex.EncodeToString(jti),
		"iss":     issuer,
		"aud":     issuer,
		"exp":     now.Add(s.cfg.JWT.AccessExpiry).Unix(),
		"iat":     now.Unix(),
		"nbf":     now.Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.cfg.JWT.Secret))
}

// ---------- UserService ----------

type UserService struct {
	userRepo UserRepository
}

func NewUserService(userRepo UserRepository) *UserService {
	return &UserService{userRepo: userRepo}
}

func (s *UserService) GetByID(ctx context.Context, id uint) (*User, error) {
	return s.userRepo.GetByID(ctx, id)
}

func (s *UserService) GetByEmail(ctx context.Context, emailStr string) (*User, error) {
	return s.userRepo.GetByEmail(ctx, emailStr)
}

func (s *UserService) GetPublicProfile(ctx context.Context, id uint) (*User, error) {
	return s.userRepo.GetPublicProfile(ctx, id)
}

// GetByIDWithAchievementsAndSubscriptions возвращает пользователя с прелоадами
// (для страницы профиля — C1, без *gorm.DB в хендлере).
func (s *UserService) GetByIDWithAchievementsAndSubscriptions(ctx context.Context, id uint) (*User, error) {
	return s.userRepo.GetByIDWithAchievementsAndSubscriptions(ctx, id)
}

func (s *UserService) UpdateProfile(ctx context.Context, id uint, name, emailStr string) error {
	fields := map[string]any{
		"name":  name,
		"email": emailStr,
	}
	// Смена email сбрасывает verified-флаг (S-L3): иначе новый адрес остаётся
	// «подтверждённым», что влияет на OAuth-линковку и другие email-trust решения.
	current, getErr := s.userRepo.GetByID(ctx, id)
	if getErr != nil {
		return getErr
	}
	if !strings.EqualFold(current.Email, emailStr) {
		fields["email_verified"] = false
	}
	return s.userRepo.Update(ctx, id, fields)
}

func (s *UserService) UpdateAvatarPath(ctx context.Context, id uint, avatarPath string) error {
	return s.userRepo.Update(ctx, id, map[string]any{
		"avatar_path": avatarPath,
	})
}

func (s *UserService) ChangePassword(ctx context.Context, id uint, oldPassword, newPassword string) error {
	user, getErr := s.userRepo.GetByID(ctx, id)
	if getErr != nil {
		return getErr
	}
	if bcryptErr := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(oldPassword)); bcryptErr != nil {
		return stderrors.New("неверный текущий пароль")
	}
	hashed, hashErr := bcrypt.GenerateFromPassword([]byte(newPassword), crypto.BcryptCost)
	if hashErr != nil {
		return hashErr
	}
	return s.userRepo.Update(ctx, id, map[string]any{"password": string(hashed)})
}

// ---------- AchievementService ----------

type AchievementService struct {
	achievRepo AchievementRepository
}

func NewAchievementService(achievRepo AchievementRepository) *AchievementService {
	return &AchievementService{achievRepo: achievRepo}
}

func (s *AchievementService) AwardAchievement(ctx context.Context, userID uint, code string) error {
	achiev := &Achievement{Code: code}
	if err := s.achievRepo.FirstOrCreate(ctx, achiev); err != nil {
		return err
	}
	return s.achievRepo.Award(ctx, userID, achiev)
}

func (s *AchievementService) GetUserAchievements(ctx context.Context, userID uint) ([]Achievement, error) {
	return s.achievRepo.GetByUserID(ctx, userID)
}

func (s *AchievementService) SeedAchievements(ctx context.Context) {
	achievements := []Achievement{
		{Code: "first_level_created", Name: "Первый уровень", Description: "Создайте свой первый уровень", Icon: "🏗️"},
		{Code: "five_games_hosted", Name: "Опытный организатор", Description: "Проведите 5 завершённых игр", Icon: "🎖️"},
		{Code: "hattrick", Name: "Хет-трик", Description: "Займите 1 место три раза подряд", Icon: "🏆"},
		{Code: "tactician", Name: "Тактик", Description: "Используйте подсказку и займите 1 место", Icon: "💡"},
		{Code: "collector", Name: "Коллекционер", Description: "Участвуйте в 10 завершённых играх", Icon: "🎮"},
		{Code: "speed_demon", Name: "Быстрый старт", Description: "Завершите игру менее чем за 5 минут", Icon: "⚡"},
	}
	for _, a := range achievements {
		if err := s.achievRepo.FirstOrCreate(ctx, &a); err != nil {
			log.Error().Err(err).Str("achievement", a.Code).Msg("SeedAchievements: failed to seed")
		}
	}
}

// ---------- OAuthService ----------

func httpClientWithTimeout(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
	}
}

type OAuthService struct {
	userRepo     UserRepository
	extLoginRepo ExternalLoginRepository
	cfg          *config.Config
	configs      map[string]*oauth2.Config
	httpClient   *http.Client
}

func NewOAuthService(
	userRepo UserRepository,
	extLoginRepo ExternalLoginRepository,
	cfg *config.Config,
) *OAuthService {
	httpClient := httpClientWithTimeout(oauthHTTPTimeout)

	configs := map[string]*oauth2.Config{
		"yandex": {
			ClientID:     cfg.OAuth.Yandex.ClientID,
			ClientSecret: cfg.OAuth.Yandex.ClientSecret,
			RedirectURL:  cfg.Server.BaseURL + "/auth/oauth/yandex/callback",
			Scopes:       []string{"login:email", "login:info"},
			Endpoint:     yandex.Endpoint,
		},
		"vk": {
			ClientID:     cfg.OAuth.VK.ClientID,
			ClientSecret: cfg.OAuth.VK.ClientSecret,
			RedirectURL:  cfg.Server.BaseURL + "/auth/oauth/vk/callback",
			Scopes:       []string{"email"},
			Endpoint: oauth2.Endpoint{
				AuthURL:  "https://oauth.vk.com/authorize",
				TokenURL: "https://oauth.vk.com/access_token",
			},
		},
	}

	return &OAuthService{
		userRepo:     userRepo,
		extLoginRepo: extLoginRepo,
		cfg:          cfg,
		configs:      configs,
		httpClient:   httpClient,
	}
}

func (s *OAuthService) GetAuthURL(provider string) (authURL string, state string, err error) {
	cfg, ok := s.configs[provider]
	if !ok {
		return "", "", stderrors.New("неподдерживаемый провайдер")
	}
	stateBytes := make([]byte, oauthStateBytes)
	if _, err := rand.Read(stateBytes); err != nil {
		return "", "", fmt.Errorf("не удалось сгенерировать state: %w", err)
	}
	state = hex.EncodeToString(stateBytes)
	authURL = cfg.AuthCodeURL(state, oauth2.AccessTypeOffline)
	return authURL, state, nil
}

type yandexUserInfo struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	IsVerified bool   `json:"is_verified"`
	FirstName  string `json:"first_name"`
	LastName   string `json:"last_name"`
}

type vkUserInfo struct {
	Response []struct {
		ID        int    `json:"id"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
	} `json:"response"`
}

func (s *OAuthService) ctxWithHTTPClient(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, s.httpClient)
}

func (s *OAuthService) Authenticate(ctx context.Context, provider, code, state string) (*User, error) {
	if state == "" {
		return nil, stderrors.New("неверный state-параметр")
	}
	cfg, ok := s.configs[provider]
	if !ok {
		return nil, stderrors.New("неподдерживаемый провайдер")
	}

	ctxWithClient := s.ctxWithHTTPClient(ctx)

	token, err := cfg.Exchange(ctxWithClient, code)
	if err != nil {
		return nil, fmt.Errorf("обмен кода на токен: %w", err)
	}

	client := cfg.Client(ctxWithClient, token)

	var emailStr, name, externalID string
	var emailVerified bool
	switch provider {
	case "yandex":
		req, reqErr := http.NewRequestWithContext(ctxWithClient, "GET", "https://login.yandex.ru/info?format=json", nil)
		if reqErr != nil {
			return nil, fmt.Errorf("создание запроса к Yandex API: %w", reqErr)
		}
		resp, respErr := client.Do(req)
		if respErr != nil {
			return nil, fmt.Errorf("запрос к Yandex API: %w", respErr)
		}
		defer func() {
			if closeErr := resp.Body.Close(); closeErr != nil {
				log.Warn().Err(closeErr).Msg("OAuth Yandex: failed to close response body")
			}
		}()
		var yInfo yandexUserInfo
		if decodeErr := json.NewDecoder(resp.Body).Decode(&yInfo); decodeErr != nil {
			return nil, fmt.Errorf("декодирование ответа Yandex: %w", decodeErr)
		}
		emailStr = yInfo.Email
		externalID = yInfo.ID
		emailVerified = yInfo.IsVerified
		if emailStr == "" {
			return nil, stderrors.New("не удалось получить email от Yandex")
		}
		if !emailVerified {
			return nil, stderrors.New("email от Yandex не подтверждён")
		}
		name = yInfo.FirstName
		if name == "" {
			name = yInfo.LastName
		}
	case "vk":
		// VK возвращает email в токене, получаем имя через users.get
		emailStr, _ = token.Extra("email").(string)
		if emailStr == "" {
			return nil, stderrors.New("не удалось получить email от VK")
		}
		externalID, _ = token.Extra("user_id").(string)

		userReq, reqErr := http.NewRequestWithContext(ctxWithClient, "GET",
			"https://api.vk.com/method/users.get?v=5.131&user_ids="+externalID, nil)
		if reqErr != nil {
			log.Warn().Err(reqErr).Str("external_id", externalID).Msg("VK: failed to create user request")
			name = emailStr
		} else {
			userResp, userErr := client.Do(userReq)
			if userErr == nil {
				defer userResp.Body.Close()
				var vkInfo vkUserInfo
				if decodeErr := json.NewDecoder(userResp.Body).Decode(&vkInfo); decodeErr == nil && len(vkInfo.Response) > 0 {
					name = vkInfo.Response[0].FirstName + " " + vkInfo.Response[0].LastName
				}
			}
		}
	default:
		return nil, stderrors.New("неподдерживаемый провайдер для получения информации")
	}
	if name == "" {
		name = emailStr
	}
	user, getUserErr := s.userRepo.GetByEmail(ctx, emailStr)
	if stderrors.Is(getUserErr, gorm.ErrRecordNotFound) {
		user = &User{
			Email:         emailStr,
			Name:          name,
			EmailVerified: emailVerified,
			Password:      "",
		}
		if createErr := s.userRepo.Create(ctx, user); createErr != nil {
			// #7: два параллельных OAuth-колбэка на новый email — один ловит
			// unique-violation; перечитываем созданного конкурента.
			var pgErr *pq.Error
			if stderrors.As(createErr, &pgErr) && pgErr.Code == "23505" {
				existing, rErr := s.userRepo.GetByEmail(ctx, emailStr)
				if rErr == nil {
					user = existing
				} else {
					return nil, fmt.Errorf("создание пользователя: %w", createErr)
				}
			} else {
				return nil, fmt.Errorf("создание пользователя: %w", createErr)
			}
		}
	} else if getUserErr != nil {
		return nil, fmt.Errorf("поиск пользователя: %w", getUserErr)
	} else {
		// S-L2: VK не подтверждает email. Привязка существующего аккаунта по
		// неверифицированному email могла бы захватить чужую учётку — отказываем.
		// Пользователь войдёт по паролю (или по passkey) и привяжет VK сам.
		if provider == "vk" && !emailVerified {
			return nil, stderrors.New("вход через VK невозможен: email не подтверждён. Войдите по паролю")
		}
		if user.Name != name {
			if updateErr := s.userRepo.Update(ctx, user.ID, map[string]any{"name": name}); updateErr != nil {
				log.Warn().Err(updateErr).Uint("user_id", user.ID).Msg("не удалось обновить имя пользователя")
			}
		}
		if !user.EmailVerified && emailVerified {
			if updateErr := s.userRepo.Update(ctx, user.ID, map[string]any{"email_verified": true}); updateErr != nil {
				log.Warn().Err(updateErr).Uint("user_id", user.ID).Msg("не удалось установить email_verified")
			}
		}
	}
	extLogin := &ExternalLogin{
		UserID:     user.ID,
		Provider:   provider,
		ExternalID: externalID,
	}
	if findErr := s.extLoginRepo.FindOrCreate(ctx, extLogin); findErr != nil {
		log.Warn().Err(findErr).Uint("user_id", user.ID).Str("provider", provider).Msg("FindOrCreate external login: failed, continuing")
	}
	return user, nil
}

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

// ---------- UserDashboardService ----------

type UserDashboardService struct {
	userRepo UserRepository
}

func NewUserDashboardService(userRepo UserRepository) *UserDashboardService {
	return &UserDashboardService{userRepo: userRepo}
}

type UserDashboard struct {
	AuthoredGames      []DashboardGame
	CaptainTeams       []DashboardTeamWithGame
	MemberTeams        []DashboardTeamWithGame
	ActivePassings     []DashboardPassingWithGame
	PendingInvitations []DashboardInvitation
}

type DashboardGame struct {
	ID      uint
	Name    string
	IsDraft bool
}

type DashboardTeamWithGame struct {
	Team DashboardTeam
	Game DashboardGame
}

type DashboardTeam struct {
	ID   uint
	Name string
}

type DashboardPassingWithGame struct {
	PassingStatus string
	TeamName      string
	GameName      string
	GameID        uint
	PassingID     uint
}

type DashboardInvitation struct {
	ID       uint
	TeamID   uint
	TeamName string
	Status   string
}

// GetDashboard собирает данные для дашборда с оптимизированными запросами.
// Использует 3 запроса вместо 7 за счёт JOIN (запросы — в репозитории, C1).
func (s *UserDashboardService) GetDashboard(ctx context.Context, userID uint) (*UserDashboard, error) {
	var dash UserDashboard

	// 1. Авторские игры
	authoredGames, err := s.userRepo.DashboardAuthoredGames(ctx, userID)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("GetDashboard: failed to get authored games")
		return &dash, fmt.Errorf("failed to get authored games: %w", err)
	}
	for _, g := range authoredGames {
		dash.AuthoredGames = append(dash.AuthoredGames, DashboardGame(g))
	}

	// 2. Единый запрос: команды + прохождения + названия игр через JOIN
	rows, err := s.userRepo.DashboardTeams(ctx, userID)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("GetDashboard: failed to get teams data")
		return &dash, err
	}

	seenTeams := make(map[uint]bool)
	for _, r := range rows {
		// Добавляем команду в список (один раз)
		if !seenTeams[r.TeamID] {
			seenTeams[r.TeamID] = true
			team := DashboardTeam{ID: r.TeamID, Name: r.TeamName}
			twg := DashboardTeamWithGame{Team: team, Game: DashboardGame{}}
			if r.CaptainID == userID {
				dash.CaptainTeams = append(dash.CaptainTeams, twg)
			} else {
				dash.MemberTeams = append(dash.MemberTeams, twg)
			}
		}
		// Активные прохождения
		if r.PassingID != 0 && r.GameName != "" &&
			(r.PassingStatus == "started" || r.PassingStatus == "accepted") {
			dash.ActivePassings = append(dash.ActivePassings, DashboardPassingWithGame{
				PassingStatus: r.PassingStatus,
				TeamName:      r.TeamName,
				GameName:      r.GameName,
				GameID:        r.GameID,
				PassingID:     r.PassingID,
			})
		}
	}

	// 3. Приглашения
	s.loadInvitations(ctx, &dash, userID)

	return &dash, nil
}

// loadInvitations загружает ожидающие приглашения в структуру дашборда.
func (s *UserDashboardService) loadInvitations(ctx context.Context, dash *UserDashboard, userID uint) {
	invitations, err := s.userRepo.DashboardInvitations(ctx, userID)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("loadInvitations: failed to load invitations")
		return
	}
	for _, inv := range invitations {
		dash.PendingInvitations = append(dash.PendingInvitations, DashboardInvitation(inv))
	}
}
