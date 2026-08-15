// internal/domain/user/service.go
//
// Note: tests use hand-rolled mocks or real DB; generated file may be incomplete
//
//go:generate go run go.uber.org/mock/mockgen -source=repository.go -destination=mock_service.go -package=user
package user

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/pkg/cache"
	"gengine-0/internal/pkg/crypto"
	"gengine-0/internal/pkg/metrics"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// A-9 (pass 31): sentinel-ошибки auth. Строки совпадают с теми, что раньше
// создавались stderrors.New — локализация через render.LocalizeError (по
// строке) продолжает работать, но теперь доступен errors.Is.
var (
	ErrInvalidCredentials    = stderrors.New("неверный email или пароль")
	ErrInvalidToken          = stderrors.New("невалидный токен")
	ErrTokenRevoked          = stderrors.New("токен был отозван")
	ErrTokenNotYetValid      = stderrors.New("токен ещё не действителен")
	ErrInvalidIssuedAt       = stderrors.New("неверная дата выдачи токена")
	ErrWrongIssuer           = stderrors.New("неверный issuer токена")
	ErrWrongAudience         = stderrors.New("неверный audience токена")
	ErrRefreshAsAccess       = stderrors.New("использование refresh-токена как access запрещено")
	ErrRefreshServiceNotInit = stderrors.New("refresh-сервис не инициализирован")
	ErrInternalServer        = stderrors.New("внутренняя ошибка сервера")
	// ErrChangePasswordWrong — неверный текущий пароль при смене (S-1, pass 35:
	// generic-ответ, не раскрывает блокировку аккаунта атакующему).
	ErrChangePasswordWrong = stderrors.New("неверный текущий пароль")
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
	// loginThrottleDelay (S-M3, PASS-8): мягкая задержка на неверный пароль —
	// замедляет брутфорс, не блокируя аккаунт (DoS через lockout ротацией IP).
	loginThrottleDelay = 300 * time.Millisecond
)

// ---------- AuthService ----------

type AuthService struct {
	userRepo         UserRepository
	achievRepo       AchievementRepository
	emailVerifRepo   EmailVerificationRepository
	refreshTokenRepo RefreshTokenRepository
	refreshSvc       *RefreshTokenService
	cfg              *config.Config
	cache            cache.CacheStore

	// emailVerifSvc — внедряется из DI (A-3, pass 31): раньше создавался
	// локально в Register, что было скрытой зависимостью вне wire.
	emailVerifSvc *EmailVerificationService

	// jtiBlacklist — in-memory fallback для отзыва JWT при отсутствии
	// Valkey/кэша (S-6, pass 31): process-local, но лучше, чем ничего.
	// При настроенном кэше используется он (shared между инстансами).
	jtiMu        sync.Mutex
	jtiBlacklist map[string]time.Time
}

// WithRefreshService связывает AuthService с RefreshTokenService (D2).
// RefreshAccessToken/GenerateRefreshToken делегируются в него.
func (s *AuthService) WithRefreshService(refreshSvc *RefreshTokenService) *AuthService {
	s.refreshSvc = refreshSvc
	return s
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
		jtiBlacklist:     make(map[string]time.Time),
	}
}

// WithCache sets the cache store for JWT blacklist support.
func (s *AuthService) WithCache(c cache.CacheStore) *AuthService {
	s.cache = c
	return s
}

// WithEmailVerificationService внедряет сервис подтверждения email (A-3, pass 31).
func (s *AuthService) WithEmailVerificationService(svc *EmailVerificationService) *AuthService {
	s.emailVerifSvc = svc
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

	// A-3 (pass 31): сервис подтверждения email внедрён из DI, не создаётся
	// локально (была скрытая зависимость вне wire).
	if s.emailVerifSvc != nil {
		if err := s.emailVerifSvc.SendVerificationEmail(ctx, user); err != nil {
			log.Warn().Err(err).Str("email", user.Email).Msg("Register: failed to send verification email")
		}
	}

	return &user, nil
}

func (s *AuthService) Login(ctx context.Context, emailStr, password string) (*User, string, error) {
	user, err := s.userRepo.GetByEmail(ctx, emailStr)
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			// Dummy bcrypt to prevent timing-based email enumeration.
			// Хэш генерируется с тем же BcryptCost (12), что и реальные пароли,
			// иначе тайминг-атака различает «нет пользователя» и «неверный пароль» (S3).
			bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(password)) //nolint:errcheck
		}
		return nil, "", ErrInvalidCredentials
	}

	// Проверка блокировки аккаунта.
	// Возвращаем ТОТ ЖЕ generic-ответ, что и для неверного пароля/несуществующего
	// email — иначе по сообщению «аккаунт заблокирован» атакующий узнаёт о
	// существовании аккаунта (oracle) (B2). Реальная причина логируется для ops.
	if user.LockedUntil != nil && time.Now().Before(*user.LockedUntil) {
		log.Debug().Uint("user_id", user.ID).Time("locked_until", *user.LockedUntil).Msg("Login: account is locked")
		return nil, "", ErrInvalidCredentials
	}

	if bcryptErr := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); bcryptErr != nil {
		// S-M3 (PASS-8): мягкий троттлинг — небольшая задержка на неверный пароль
		// (дороже брутфорс, не блокирует легитимного пользователя).
		time.Sleep(loginThrottleDelay)
		// Атомарный инкремент счётчика неудачных попыток (без race condition)
		newAttempts, incErr := s.userRepo.AtomicIncrementFailedAttempts(ctx, user.ID)
		if incErr != nil {
			log.Error().Err(incErr).Uint("user_id", user.ID).Msg("Login: atomic increment failed")
			return nil, "", ErrInternalServer
		}

		if newAttempts >= 5 {
			// S-4 (pass 31/32): экспоненциальный backoff — длительность блокировки
			// растёт с каждой последовательной блокировкой (5, 10, 20, ... мин,
			// до 24ч). Атомарный инкремент lock_count (S-4 pass 32) — без гонки
			// между параллельными неверными попытками.
			// DEEP-REVIEW MEDIUM #19 (pass 46): длительность вычисляется ВНУТРИ
			// SQL по фактическому lock_count (раньше — по устаревшему
			// user.LockCount: при параллельных попытках обе получали 5 мин).
			newCount, lockErr := s.userRepo.LockAccountWithBackoff(ctx, user.ID)
			if lockErr != nil {
				log.Error().Err(lockErr).Uint("user_id", user.ID).Msg("Login: failed to lock account")
				return nil, "", ErrInternalServer
			}
			log.Debug().Uint("user_id", user.ID).Int("lock_count", newCount).Msg("Login: account locked with backoff")
			// Generic-ответ: не раскрываем существование аккаунта (B2).
			return nil, "", ErrInvalidCredentials
		}
		return nil, "", ErrInvalidCredentials
	}

	// Успешный вход — сброс счётчика и backoff-счётчика блокировок.
	// DEEP-REVIEW PASS-2 (#15): условный сброс (только если были попытки/
	// блокировка) — раньше писали UPDATE на каждый успешный логин.
	if resetErr := s.userRepo.ResetLoginAttemptsIfNeeded(ctx, user.ID); resetErr != nil {
		// L1 (PASS-17): логировали err (nil), а не resetErr.
		log.Error().Err(resetErr).Uint("user_id", user.ID).Msg("Login: failed to reset failed_login_attempts")
	}

	// DEEP-REVIEW PASS-2 (#14): возвращаем user, чтобы handler не делал
	// повторный GetByEmail для 2FA-проверки.
	token, err := s.generateJWT(*user)
	if err != nil {
		return nil, "", err
	}
	return user, token, nil
}

func (s *AuthService) GenerateJWT(user User) (string, error) {
	return s.generateJWT(user)
}

// backoffDuration возвращает длительность блокировки аккаунта (S-4, pass 31):
// 5 мин при первой блокировке, удваивается с каждой последующей.
// S-M3 (PASS-8): максимум снижен 24ч → 1ч — атакующий с ротируемых IP
// блокирует жертву максимум на час (раньше — на сутки; DoS-ущерб меньше,
// а экспонента всё ещё тормозит настоящий брутфорс).
func backoffDuration(lockCount int) time.Duration {
	const (
		baseLockDuration = 5 * time.Minute
		maxLockDuration  = 1 * time.Hour
	)
	if lockCount <= 0 {
		return baseLockDuration
	}
	// lockCount-ая блокировка: base * 2^(lockCount). Переполнение страхуем.
	if lockCount >= 5 { // 5 мин * 2^5 = 2.5ч → уже за cap
		return maxLockDuration
	}
	d := baseLockDuration << uint(lockCount) // 5 * 2^lockCount
	if d > maxLockDuration {
		return maxLockDuration
	}
	return d
}

// ---------- Refresh-токены: делегируются RefreshTokenService (D2) ----------

// GenerateRefreshToken создаёт refresh-токен для новой сессии.
func (s *AuthService) GenerateRefreshToken(ctx context.Context, user User, deviceID, clientFingerprint string) (string, error) {
	if s.refreshSvc == nil {
		return "", ErrRefreshServiceNotInit
	}
	return s.refreshSvc.GenerateRefreshToken(ctx, user, deviceID, clientFingerprint)
}

func (s *AuthService) RevokeAllUserTokens(ctx context.Context, userID uint) error {
	if s.refreshSvc == nil {
		return ErrRefreshServiceNotInit
	}
	return s.refreshSvc.RevokeAllUserTokens(ctx, userID)
}

func (s *AuthService) RevokeRefreshToken(ctx context.Context, refreshTokenStr string) error {
	if s.refreshSvc == nil {
		return ErrRefreshServiceNotInit
	}
	return s.refreshSvc.RevokeRefreshToken(ctx, refreshTokenStr)
}

func (s *AuthService) CleanExpiredRefreshTokens(ctx context.Context) error {
	if s.refreshSvc == nil {
		return ErrRefreshServiceNotInit
	}
	return s.refreshSvc.CleanExpiredRefreshTokens(ctx)
}

func (s *AuthService) RefreshAccessToken(ctx context.Context, refreshTokenStr, deviceID, clientFingerprint string) (string, string, error) {
	if s.refreshSvc == nil {
		return "", "", ErrRefreshServiceNotInit
	}
	return s.refreshSvc.RefreshAccessToken(ctx, refreshTokenStr, deviceID, clientFingerprint)
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
		return 0, "", ErrInvalidToken
	}

	// Проверяем, что токен не refresh-токен
	if isRefresh, ok := claims["refresh"].(bool); ok && isRefresh {
		return 0, "", ErrRefreshAsAccess
	}

	// Проверяем nbf (not before)
	if nbf, ok := claims["nbf"].(float64); ok {
		if time.Now().Unix() < int64(nbf) {
			return 0, "", ErrTokenNotYetValid
		}
	}

	// Проверяем iat (issued at)
	if iat, ok := claims["iat"].(float64); ok {
		if time.Now().Unix() < int64(iat) {
			return 0, "", ErrInvalidIssuedAt
		}
	}

	// Проверяем issuer/audience — токены, выпущенные другим сервисом/окружением,
	// не принимаем даже при совпадении секрета (S1).
	expectedIssuer := s.cfg.Server.BaseURL
	if expectedIssuer == "" {
		expectedIssuer = "gengine"
	}
	if iss, ok := claims["iss"].(string); !ok || iss != expectedIssuer {
		return 0, "", ErrWrongIssuer
	}
	if aud, ok := claims["aud"].(string); !ok || aud != expectedIssuer {
		return 0, "", ErrWrongAudience
	}

	// JTI blacklist check: если токен был отозван (logout, password change), отклоняем его
	if jti, ok := claims["jti"].(string); ok && jti != "" {
		if s.cache != nil {
			if _, found := s.cache.Get("jti_blacklist:" + jti); found {
				return 0, "", ErrTokenRevoked
			}
		} else {
			// S-6 (pass 31): in-memory fallback при отсутствии кэша.
			s.jtiMu.Lock()
			exp, found := s.jtiBlacklist[jti]
			if found && time.Now().Before(exp) {
				s.jtiMu.Unlock()
				return 0, "", ErrTokenRevoked
			}
			if found && !time.Now().Before(exp) {
				delete(s.jtiBlacklist, jti)
			}
			s.jtiMu.Unlock()
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
		return
	}
	// S-6 (pass 31): без кэша используем in-memory blacklist — отзыв работает
	// в рамках процесса (fallback лучше, чем «не работает вовсе»).
	s.jtiMu.Lock()
	s.jtiBlacklist[jti] = time.Now().Add(ttl)
	// Lazy sweep: не даём map расти (P-2 паттерн).
	if len(s.jtiBlacklist) > 1024 {
		now := time.Now()
		for k, exp := range s.jtiBlacklist {
			if now.After(exp) {
				delete(s.jtiBlacklist, k)
			}
		}
	}
	s.jtiMu.Unlock()
}

func (s *AuthService) generateJWT(user User) (string, error) {
	// JTI: уникальный ID токена для отзыва (jti blacklist через RevokeJWT +
	// кэш, реализовано; при отсутствии кэша — warn в RevokeJWT, S-6).
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

// AtomicIncrementFailedAttempts инкрементирует счётчик неудачных попыток
// (S-3, pass 31: используется и в Login, и в 2FA-верификации).
func (s *UserService) AtomicIncrementFailedAttempts(ctx context.Context, userID uint) (int, error) {
	return s.userRepo.AtomicIncrementFailedAttempts(ctx, userID)
}

// SetLockedUntil блокирует аккаунт с экспоненциальным backoff (S-4, pass 31):
// длительность зависит от текущего lock_count, счётчик инкрементируется.
func (s *UserService) SetLockedUntil(ctx context.Context, userID uint) error {
	// S-5 (pass 32) + S-L1 (PASS-8): атомарный backoff-лок (как в Login) —
	// инкремент lock_count и расчёт длительности ВНУТРИ одного UPDATE. Раньше
	// читали устаревший user.LockCount (гонка между параллельными попытками).
	_, err := s.userRepo.LockAccountWithBackoff(ctx, userID)
	return err
}

// ResetFailedAttempts сбрасывает счётчик неудачных попыток и блокировку.
func (s *UserService) ResetFailedAttempts(ctx context.Context, userID uint) error {
	return s.userRepo.Update(ctx, userID, map[string]any{
		"failed_login_attempts": 0,
		"locked_until":          nil,
		"lock_count":            0,
	})
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

// UpdateAvatarPath обновляет путь аватара. Возвращает ПРЕЖНИЙ путь (L12, PASS-3)
// — хендлер удаляет старый файл, чтобы не копить мусор на диске.
func (s *UserService) UpdateAvatarPath(ctx context.Context, id uint, avatarPath string) (string, error) {
	user, getErr := s.userRepo.GetByID(ctx, id)
	if getErr != nil {
		return "", getErr
	}
	oldPath := user.AvatarPath
	if err := s.userRepo.Update(ctx, id, map[string]any{
		"avatar_path": avatarPath,
	}); err != nil {
		return "", err
	}
	return oldPath, nil
}

func (s *UserService) ChangePassword(ctx context.Context, id uint, oldPassword, newPassword string) error {
	user, getErr := s.userRepo.GetByID(ctx, id)
	if getErr != nil {
		return getErr
	}

	// S-1 (pass 35): lockout на смену пароля — тот же паттерн, что и в Login.
	// Иначе украденная JWT-кука даёт бесконечный перебор старого пароля.
	if user.LockedUntil != nil && time.Now().Before(*user.LockedUntil) {
		log.Debug().Uint("user_id", user.ID).Time("locked_until", *user.LockedUntil).Msg("ChangePassword: account is locked")
		return ErrChangePasswordWrong
	}

	if bcryptErr := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(oldPassword)); bcryptErr != nil {
		// S-M3 (PASS-8): мягкий троттлинг на неверный старый пароль.
		time.Sleep(loginThrottleDelay)
		// Атомарный инкремент счётчика неудачных попыток (как в Login).
		newAttempts, incErr := s.userRepo.AtomicIncrementFailedAttempts(ctx, user.ID)
		if incErr != nil {
			log.Error().Err(incErr).Uint("user_id", user.ID).Msg("ChangePassword: atomic increment failed")
			return incErr
		}
		if newAttempts >= 5 {
			// S-L1 (PASS-8): LockAccountWithBackoff — длительность считается ВНУТРИ
			// SQL по фактическому lock_count. Раньше читали устаревший user.LockCount
			// (гонка: два параллельных неверных ввода оба ставили 5 мин вместо backoff).
			newCount, lockErr := s.userRepo.LockAccountWithBackoff(ctx, user.ID)
			if lockErr != nil {
				log.Error().Err(lockErr).Uint("user_id", user.ID).Msg("ChangePassword: failed to lock account")
				return lockErr
			}
			log.Debug().Uint("user_id", user.ID).Int("lock_count", newCount).Msg("ChangePassword: account locked with backoff")
		}
		return ErrChangePasswordWrong
	}

	// Успешная смена пароля — сброс счётчика неудач и блокировки.
	if err := s.userRepo.Update(ctx, id, map[string]any{
		"failed_login_attempts": 0,
		"locked_until":          nil,
		"lock_count":            0,
	}); err != nil {
		log.Error().Err(err).Uint("user_id", user.ID).Msg("ChangePassword: failed to reset failed attempts")
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
