// internal/domain/user/service.go
//
//go:generate go run go.uber.org/mock/mockgen -source=service.go -destination=mock_service.go -package=user
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
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/pkg/crypto"
	"gengine-0/internal/pkg/email"
	errspkg "gengine-0/internal/pkg/errors"
	"gengine-0/internal/pkg/metrics"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
	"golang.org/x/oauth2/google"
	"golang.org/x/oauth2/yandex"
	"gorm.io/gorm"
)

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
		return "", stderrors.New("неверный email или пароль")
	}

	// Проверка блокировки аккаунта
	if user.LockedUntil != nil && time.Now().Before(*user.LockedUntil) {
		remaining := time.Until(*user.LockedUntil).Truncate(time.Second)
		return "", fmt.Errorf("аккаунт заблокирован до %s (осталось %s)", user.LockedUntil.Format("15:04:05"), remaining)
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
			return "", fmt.Errorf("аккаунт заблокирован на 30 минут (превышено 5 неудачных попыток)")
		}
		return "", stderrors.New("неверный email или пароль")
	}

	// Успешный вход — сброс счётчика (с проверкой ошибки)
	if user.FailedLoginAttempts > 0 || user.LockedUntil != nil {
		if err := s.userRepo.Update(ctx, user.ID, map[string]any{"failed_login_attempts": 0, "locked_until": nil}); err != nil {
			log.Error().Err(err).Uint("user_id", user.ID).Msg("Login: failed to reset failed_login_attempts")
		}
	}

	return s.generateJWT(*user)
}

func (s *AuthService) GenerateJWT(user User) (string, error) {
	return s.generateJWT(user)
}

func (s *AuthService) GenerateRefreshToken(ctx context.Context, user User, deviceID string) (string, error) {
	b := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	hash := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(hash[:])

	refreshToken := &RefreshToken{
		UserID:    user.ID,
		TokenHash: tokenHash,
		DeviceID:  deviceID,
		ExpiresAt: time.Now().Add(s.cfg.JWT.RefreshExpiry),
	}
	if err := s.refreshTokenRepo.Create(ctx, refreshToken); err != nil {
		return "", err
	}
	return token, nil
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

func (s *AuthService) RefreshAccessToken(ctx context.Context, refreshTokenStr string) (string, error) {
	hash := sha256.Sum256([]byte(refreshTokenStr))
	tokenHash := hex.EncodeToString(hash[:])

	stored, err := s.refreshTokenRepo.GetByTokenHash(ctx, tokenHash)
	if err != nil {
		return "", stderrors.New("невалидный или отозванный refresh-токен")
	}
	if stored.ExpiresAt.Before(time.Now()) {
		return "", stderrors.New("refresh-токен истёк")
	}

	user, err := s.userRepo.GetByID(ctx, stored.UserID)
	if err != nil {
		return "", stderrors.New("пользователь не найден")
	}

	return s.generateJWT(*user)
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

	// Проверяем nbf (not before) — jwt.ParseWithClaims с MapClaims не проверяет автоматически
	if nbf, ok := claims["nbf"].(float64); ok {
		if time.Now().Unix() < int64(nbf) {
			return 0, "", stderrors.New("токен ещё не действителен")
		}
	}

	// Проверяем iat (issued at) — токен не должен быть выдан в будущем
	if iat, ok := claims["iat"].(float64); ok {
		if time.Now().Unix() < int64(iat) {
			return 0, "", stderrors.New("неверная дата выдачи токена")
		}
	}

	// Проверяем user_id с проверкой типа
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

func (s *AuthService) generateJWT(user User) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"user_id": user.ID,
		"email":   user.Email,
		"role":    user.Role,
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

func (s *UserService) UpdateProfile(ctx context.Context, id uint, name, emailStr string) error {
	return s.userRepo.Update(ctx, id, map[string]any{
		"name":  name,
		"email": emailStr,
	})
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
		"google": {
			ClientID:     cfg.OAuth.Google.ClientID,
			ClientSecret: cfg.OAuth.Google.ClientSecret,
			RedirectURL:  cfg.Server.BaseURL + "/auth/oauth/google/callback",
			Scopes:       []string{"email", "profile"},
			Endpoint:     google.Endpoint,
		},
		"github": {
			ClientID:     cfg.OAuth.GitHub.ClientID,
			ClientSecret: cfg.OAuth.GitHub.ClientSecret,
			RedirectURL:  cfg.Server.BaseURL + "/auth/oauth/github/callback",
			Scopes:       []string{"user:email"},
			Endpoint:     github.Endpoint,
		},
		"yandex": {
			ClientID:     cfg.OAuth.Yandex.ClientID,
			ClientSecret: cfg.OAuth.Yandex.ClientSecret,
			RedirectURL:  cfg.Server.BaseURL + "/auth/oauth/yandex/callback",
			Scopes:       []string{"login:email", "login:info"},
			Endpoint:     yandex.Endpoint,
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

type googleUserInfo struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	VerifiedEmail bool   `json:"verified_email"`
	Name          string `json:"name"`
}

type githubEmail struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

type yandexUserInfo struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	IsVerified bool   `json:"is_verified"`
	FirstName  string `json:"first_name"`
	LastName   string `json:"last_name"`
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
	case "google":
		req, reqErr := http.NewRequestWithContext(ctxWithClient, "GET", "https://www.googleapis.com/oauth2/v2/userinfo", nil)
		if reqErr != nil {
			return nil, fmt.Errorf("создание запроса к Google API: %w", reqErr)
		}
		resp, respErr := client.Do(req)
		if respErr != nil {
			return nil, fmt.Errorf("запрос к Google API: %w", respErr)
		}
		defer func() {
			if closeErr := resp.Body.Close(); closeErr != nil {
				log.Warn().Err(closeErr).Msg("OAuth Google: failed to close response body")
			}
		}()
		var info googleUserInfo
		if decodeErr := json.NewDecoder(resp.Body).Decode(&info); decodeErr != nil {
			return nil, fmt.Errorf("декодирование ответа Google: %w", decodeErr)
		}
		emailStr = info.Email
		externalID = info.ID
		emailVerified = info.VerifiedEmail
		name = info.Name
		if emailStr == "" {
			return nil, stderrors.New("не удалось получить email от Google")
		}
		if !emailVerified {
			return nil, stderrors.New("email от Google не подтверждён")
		}
	case "github":
		req, reqErr := http.NewRequestWithContext(ctxWithClient, "GET", "https://api.github.com/user/emails", nil)
		if reqErr != nil {
			return nil, fmt.Errorf("создание запроса к GitHub API: %w", reqErr)
		}
		resp, respErr := client.Do(req)
		if respErr != nil {
			return nil, fmt.Errorf("запрос к GitHub API: %w", respErr)
		}
		defer func() {
			if closeErr := resp.Body.Close(); closeErr != nil {
				log.Warn().Err(closeErr).Msg("OAuth GitHub: failed to close response body")
			}
		}()
		var emails []githubEmail
		if decodeErr := json.NewDecoder(resp.Body).Decode(&emails); decodeErr != nil {
			return nil, fmt.Errorf("декодирование ответа GitHub: %w", decodeErr)
		}
		var found bool
		for _, e := range emails {
			if e.Primary && e.Verified {
				emailStr = e.Email
				found = true
				break
			}
		}
		if !found {
			return nil, stderrors.New("не найден верифицированный primary email от GitHub")
		}
		reqUser, reqUserErr := http.NewRequestWithContext(ctxWithClient, "GET", "https://api.github.com/user", nil)
		if reqUserErr != nil {
			return nil, fmt.Errorf("создание запроса к GitHub user: %w", reqUserErr)
		}
		respUser, respUserErr := client.Do(reqUser)
		if respUserErr != nil {
			log.Warn().Err(respUserErr).Msg("не удалось получить имя пользователя GitHub")
		} else {
			defer func() {
				if closeErr := respUser.Body.Close(); closeErr != nil {
					log.Warn().Err(closeErr).Msg("OAuth GitHub user: failed to close response body")
				}
			}()
			var userInfo struct {
				Login string `json:"login"`
				ID    uint   `json:"id"`
			}
			if decodeErr := json.NewDecoder(respUser.Body).Decode(&userInfo); decodeErr == nil {
				name = userInfo.Login
				externalID = fmt.Sprintf("%d", userInfo.ID)
			}
		}
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
		var info yandexUserInfo
		if decodeErr := json.NewDecoder(resp.Body).Decode(&info); decodeErr != nil {
			return nil, fmt.Errorf("декодирование ответа Yandex: %w", decodeErr)
		}
		emailStr = info.Email
		externalID = info.ID
		emailVerified = info.IsVerified
		if emailStr == "" {
			return nil, stderrors.New("не удалось получить email от Yandex")
		}
		if !emailVerified {
			return nil, stderrors.New("email от Yandex не подтверждён")
		}
		name = info.FirstName
		if name == "" {
			name = info.LastName
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
			EmailVerified: true,
			Password:      "",
		}
		if createErr := s.userRepo.Create(ctx, user); createErr != nil {
			return nil, fmt.Errorf("создание пользователя: %w", createErr)
		}
	} else if getUserErr != nil {
		return nil, fmt.Errorf("поиск пользователя: %w", getUserErr)
	} else {
		if user.Name != name {
			if updateErr := s.userRepo.Update(ctx, user.ID, map[string]any{"name": name}); updateErr != nil {
				log.Warn().Err(updateErr).Uint("user_id", user.ID).Msg("не удалось обновить имя пользователя")
			}
		}
		if !user.EmailVerified {
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
	if err := s.userRepo.Update(ctx, token.UserID, map[string]any{"password": string(hashed)}); err != nil {
		return err
	}
	if err := s.passResetRepo.MarkTokenUsed(ctx, token.ID, now); err != nil {
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

	b := make([]byte, emailVerificationTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return fmt.Errorf("не удалось сгенерировать токен верификации: %w", err)
	}
	token := hex.EncodeToString(b)
	hash := sha256.Sum256([]byte(token))
	if err := s.emailVerifRepo.CreateToken(ctx, &EmailVerificationToken{
		UserID:    user.ID,
		TokenHash: hex.EncodeToString(hash[:]),
		ExpiresAt: time.Now().Add(emailVerificationExpiry),
	}); err != nil {
		return fmt.Errorf("не удалось сохранить токен верификации: %w", err)
	}
	if err := email.Enqueue(
		user.Email,
		"Подтверждение email",
		fmt.Sprintf("Перейдите по ссылке для подтверждения: %s/auth/verify?token=%s", s.cfg.Server.BaseURL, token),
	); err != nil {
		log.Error().Err(err).Str("email", user.Email).Msg("SendVerificationEmail: failed to enqueue email")
		// Удаляем токен, так как письмо не ушло
		errspkg.LogSilently(s.emailVerifRepo.DeleteToken(ctx, &EmailVerificationToken{TokenHash: hex.EncodeToString(hash[:])}), "SendVerificationEmail: cleanup failed")
		return fmt.Errorf("не удалось отправить письмо: %w", err)
	}
	return nil
}

func (s *EmailVerificationService) VerifyToken(ctx context.Context, tokenStr string) (*User, error) {
	token, err := s.emailVerifRepo.GetToken(ctx, tokenStr)
	if err != nil {
		return nil, stderrors.New("токен недействителен или истёк")
	}
	if token.ExpiresAt.Before(time.Now()) {
		return nil, stderrors.New("токен истёк")
	}
	if err := s.userRepo.Update(ctx, token.UserID, map[string]any{"email_verified": true}); err != nil {
		return nil, err
	}
	errspkg.LogSilently(s.emailVerifRepo.DeleteToken(ctx, token), "VerifyToken: cleanup failed")
	return s.userRepo.GetByID(ctx, token.UserID)
}

// ---------- UserDashboardService ----------

type UserDashboardService struct {
	DB *gorm.DB
}

func NewUserDashboardService(db *gorm.DB) *UserDashboardService {
	return &UserDashboardService{DB: db}
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
// Использует 3 запроса вместо 7 за счёт JOIN.
func (s *UserDashboardService) GetDashboard(ctx context.Context, userID uint) (*UserDashboard, error) {
	var dash UserDashboard

	// 1. Авторские игры
	var authoredGames []struct {
		ID      uint
		Name    string
		IsDraft bool
	}
	if err := s.DB.WithContext(ctx).Table("games").
		Select("id, name, is_draft").
		Where("author_id = ? AND deleted_at IS NULL", userID).
		Find(&authoredGames).Error; err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("GetDashboard: failed to get authored games")
		return &dash, fmt.Errorf("failed to get authored games: %w", err)
	}
	for _, g := range authoredGames {
		dash.AuthoredGames = append(dash.AuthoredGames, DashboardGame{
			ID:      g.ID,
			Name:    g.Name,
			IsDraft: g.IsDraft,
		})
	}

	// 2. Единый запрос: команды + прохождения + названия игр через JOIN
	type teamRow struct {
		TeamID        uint
		TeamName      string
		CaptainID     uint
		PassingID     uint
		GameID        uint
		PassingStatus string
		GameName      string
	}
	var rows []teamRow
	if err := s.DB.WithContext(ctx).Raw(`
		SELECT t.id as team_id, t.name as team_name, t.captain_id,
		       COALESCE(gp.id, 0) as passing_id,
		       COALESCE(gp.game_id, 0) as game_id,
		       COALESCE(gp.status, '') as passing_status,
		       COALESCE(g.name, '') as game_name
		FROM teams t
		LEFT JOIN game_passings gp ON gp.team_id = t.id AND gp.status IN ('accepted', 'started', 'finished')
		LEFT JOIN games g ON g.id = gp.game_id AND g.deleted_at IS NULL
		WHERE t.id IN (
			SELECT id FROM teams WHERE captain_id = ?
			UNION
			SELECT t.id FROM teams t
			INNER JOIN team_members tm ON tm.team_id = t.id
			WHERE tm.user_id = ? AND t.captain_id != ?
		)
	`, userID, userID, userID).Scan(&rows).Error; err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("GetDashboard: failed to get teams data")
		return &dash, err
	}

	for _, r := range rows {
		if r.PassingID == 0 || r.GameName == "" {
			continue
		}
		team := DashboardTeam{ID: r.TeamID, Name: r.TeamName}
		game := DashboardGame{ID: r.GameID, Name: r.GameName}
		twg := DashboardTeamWithGame{Team: team, Game: game}
		if r.CaptainID == userID {
			dash.CaptainTeams = append(dash.CaptainTeams, twg)
		} else {
			dash.MemberTeams = append(dash.MemberTeams, twg)
		}
		if r.PassingStatus == "started" || r.PassingStatus == "accepted" {
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
	var invitations []DashboardInvitation
	if err := s.DB.WithContext(ctx).Table("invitations").
		Select("invitations.id, invitations.team_id, teams.name as team_name, invitations.status").
		Joins("JOIN teams ON teams.id = invitations.team_id").
		Where("invitations.user_id = ? AND invitations.status = ?", userID, "pending").
		Scan(&invitations).Error; err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("loadInvitations: failed to load invitations")
	} else {
		dash.PendingInvitations = invitations
	}
}
