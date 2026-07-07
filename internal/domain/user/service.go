// internal/domain/user/service.go
package user

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/pkg/email"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
	"golang.org/x/oauth2/google"
	"golang.org/x/oauth2/yandex"
	"gorm.io/gorm"
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
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
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

	verificationService := NewEmailVerificationService(s.userRepo, s.emailVerifRepo, s.cfg)
	if err := verificationService.SendVerificationEmail(ctx, user); err != nil {
		log.Error().Err(err).Str("email", user.Email).Msg("не удалось отправить письмо подтверждения email")
	}

	return &user, nil
}

func (s *AuthService) Login(ctx context.Context, emailStr, password string) (string, error) {
	user, err := s.userRepo.GetByEmail(ctx, emailStr)
	if err != nil {
		return "", errors.New("неверный email или пароль")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		return "", errors.New("неверный email или пароль")
	}
	return s.generateJWT(*user)
}

func (s *AuthService) GenerateJWT(user User) (string, error) {
	return s.generateJWT(user)
}

func (s *AuthService) GenerateRefreshToken(user User, deviceID string) (string, error) {
	b := make([]byte, 32)
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
	if err := s.refreshTokenRepo.Create(context.Background(), refreshToken); err != nil {
		return "", err
	}
	return token, nil
}

func (s *AuthService) RevokeAllUserTokens(ctx context.Context, userID uint) error {
	return s.refreshTokenRepo.RevokeAllForUser(ctx, userID)
}

func (s *AuthService) CleanExpiredRefreshTokens(ctx context.Context) error {
	return s.refreshTokenRepo.DeleteExpired(ctx)
}

func (s *AuthService) RefreshAccessToken(refreshTokenStr string) (string, error) {
	hash := sha256.Sum256([]byte(refreshTokenStr))
	tokenHash := hex.EncodeToString(hash[:])

	stored, err := s.refreshTokenRepo.GetByTokenHash(context.Background(), tokenHash)
	if err != nil {
		return "", errors.New("невалидный или отозванный refresh-токен")
	}
	if stored.ExpiresAt.Before(time.Now()) {
		return "", errors.New("refresh-токен истёк")
	}

	user, err := s.userRepo.GetByID(context.Background(), stored.UserID)
	if err != nil {
		return "", errors.New("пользователь не найден")
	}

	return s.generateJWT(*user)
}

func (s *AuthService) ParseToken(tokenStr string) (uint, string, error) {
	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("неверный метод подписи")
		}
		return []byte(s.cfg.JWT.Secret), nil
	})
	if err != nil || !token.Valid {
		return 0, "", errors.New("невалидный токен")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return 0, "", errors.New("неверные данные токена")
	}
	if isRefresh, ok := claims["refresh"].(bool); ok && isRefresh {
		return 0, "", errors.New("использование refresh-токена как access запрещено")
	}
	userIDFloat, ok := claims["user_id"].(float64)
	if !ok {
		return 0, "", errors.New("неверный ID пользователя в токене")
	}
	role, _ := claims["role"].(string)
	return uint(userIDFloat), role, nil
}

func (s *AuthService) generateJWT(user User) (string, error) {
	claims := jwt.MapClaims{
		"user_id": user.ID,
		"email":   user.Email,
		"role":    user.Role,
		"exp":     time.Now().Add(s.cfg.JWT.AccessExpiry).Unix(),
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

func (s *UserService) ChangePassword(ctx context.Context, id uint, oldPassword, newPassword string) error {
	user, err := s.userRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(oldPassword)); err != nil {
		return errors.New("неверный текущий пароль")
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
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
	httpClient := httpClientWithTimeout(15 * time.Second)

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
		return "", "", errors.New("неподдерживаемый провайдер")
	}
	stateBytes := make([]byte, 16)
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
	if len(state) != 32 {
		return nil, errors.New("неверный state-параметр")
	}
	cfg, ok := s.configs[provider]
	if !ok {
		return nil, errors.New("неподдерживаемый провайдер")
	}

	ctxWithClient := s.ctxWithHTTPClient(ctx)

	token, err := cfg.Exchange(ctxWithClient, code)
	if err != nil {
		return nil, fmt.Errorf("обмен кода на токен: %w", err)
	}

	client := cfg.Client(ctxWithClient, token)

	var emailStr, name string
	var emailVerified bool
	switch provider {
	case "google":
		req, err := http.NewRequestWithContext(ctxWithClient, "GET", "https://www.googleapis.com/oauth2/v2/userinfo", nil)
		if err != nil {
			return nil, fmt.Errorf("создание запроса к Google API: %w", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("запрос к Google API: %w", err)
		}
		defer func() {
			if closeErr := resp.Body.Close(); closeErr != nil {
				log.Warn().Err(closeErr).Msg("OAuth Google: failed to close response body")
			}
		}()
		var info googleUserInfo
		if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
			return nil, fmt.Errorf("декодирование ответа Google: %w", err)
		}
		emailStr = info.Email
		emailVerified = info.VerifiedEmail
		name = info.Name
		if emailStr == "" {
			return nil, errors.New("не удалось получить email от Google")
		}
		if !emailVerified {
			return nil, errors.New("email от Google не подтверждён")
		}
	case "github":
		req, err := http.NewRequestWithContext(ctxWithClient, "GET", "https://api.github.com/user/emails", nil)
		if err != nil {
			return nil, fmt.Errorf("создание запроса к GitHub API: %w", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("запрос к GitHub API: %w", err)
		}
		defer func() {
			if closeErr := resp.Body.Close(); closeErr != nil {
				log.Warn().Err(closeErr).Msg("OAuth GitHub: failed to close response body")
			}
		}()
		var emails []githubEmail
		if err := json.NewDecoder(resp.Body).Decode(&emails); err != nil {
			return nil, fmt.Errorf("декодирование ответа GitHub: %w", err)
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
			return nil, errors.New("не найден верифицированный primary email от GitHub")
		}
		reqUser, err := http.NewRequestWithContext(ctxWithClient, "GET", "https://api.github.com/user", nil)
		if err != nil {
			return nil, fmt.Errorf("создание запроса к GitHub user: %w", err)
		}
		respUser, err := client.Do(reqUser)
		if err != nil {
			log.Warn().Err(err).Msg("не удалось получить имя пользователя GitHub")
		} else {
			defer func() {
				if closeErr := respUser.Body.Close(); closeErr != nil {
					log.Warn().Err(closeErr).Msg("OAuth GitHub user: failed to close response body")
				}
			}()
			var userInfo struct {
				Login string `json:"login"`
			}
			if err := json.NewDecoder(respUser.Body).Decode(&userInfo); err == nil {
				name = userInfo.Login
			}
		}
	case "yandex":
		req, err := http.NewRequestWithContext(ctxWithClient, "GET", "https://login.yandex.ru/info?format=json", nil)
		if err != nil {
			return nil, fmt.Errorf("создание запроса к Yandex API: %w", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("запрос к Yandex API: %w", err)
		}
		defer func() {
			if closeErr := resp.Body.Close(); closeErr != nil {
				log.Warn().Err(closeErr).Msg("OAuth Yandex: failed to close response body")
			}
		}()
		var info yandexUserInfo
		if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
			return nil, fmt.Errorf("декодирование ответа Yandex: %w", err)
		}
		emailStr = info.Email
		emailVerified = info.IsVerified
		if emailStr == "" {
			return nil, errors.New("не удалось получить email от Yandex")
		}
		if !emailVerified {
			return nil, errors.New("email от Yandex не подтверждён")
		}
		name = info.FirstName
		if name == "" {
			name = info.LastName
		}
	default:
		return nil, errors.New("неподдерживаемый провайдер для получения информации")
	}
	if name == "" {
		name = emailStr
	}
	user, err := s.userRepo.GetByEmail(ctx, emailStr)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		user = &User{
			Email:         emailStr,
			Name:          name,
			EmailVerified: true,
			Password:      "",
		}
		if err := s.userRepo.Create(ctx, user); err != nil {
			return nil, fmt.Errorf("создание пользователя: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("поиск пользователя: %w", err)
	} else {
		if user.Name != name {
			if err := s.userRepo.Update(ctx, user.ID, map[string]any{"name": name}); err != nil {
				log.Warn().Err(err).Uint("user_id", user.ID).Msg("не удалось обновить имя пользователя")
			}
		}
		if !user.EmailVerified {
			if err := s.userRepo.Update(ctx, user.ID, map[string]any{"email_verified": true}); err != nil {
				log.Warn().Err(err).Uint("user_id", user.ID).Msg("не удалось установить email_verified")
			}
		}
	}
	extLogin := &ExternalLogin{
		UserID:     user.ID,
		Provider:   provider,
		ExternalID: emailStr,
	}
	if err := s.extLoginRepo.FindOrCreate(ctx, extLogin); err != nil {
		return nil, fmt.Errorf("привязка внешнего аккаунта (%s): %w", provider, err)
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
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("генерация токена сброса пароля: %w", err)
	}
	rawToken := hex.EncodeToString(b)
	token := PasswordResetToken{
		UserID:    user.ID,
		Token:     rawToken,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	if err := s.passResetRepo.CreateToken(ctx, &token); err != nil {
		return "", err
	}
	if s.cfg.SMTP.Enabled {
		if err := email.Enqueue(
			user.Email,
			"Сброс пароля",
			fmt.Sprintf("Для сброса пароля перейдите по ссылке: %s/auth/reset?token=%s", s.cfg.Server.BaseURL, rawToken),
		); err != nil {
			log.Error().Err(err).Str("email", user.Email).Msg("failed to enqueue password reset email")
		}
	}
	return rawToken, nil
}

func (s *PasswordResetService) ResetPassword(ctx context.Context, tokenStr, newPassword string) error {
	token, err := s.passResetRepo.GetToken(ctx, tokenStr)
	if err != nil {
		return errors.New("токен недействителен или истёк")
	}
	if token.ExpiresAt.Before(time.Now()) {
		return errors.New("токен истёк")
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
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
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Errorf("генерация токена подтверждения email: %w", err)
	}
	token := hex.EncodeToString(b)
	if err := s.emailVerifRepo.CreateToken(ctx, &EmailVerificationToken{
		UserID:    user.ID,
		Token:     token,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}); err != nil {
		return fmt.Errorf("сохранение токена подтверждения email: %w", err)
	}
	if s.cfg.SMTP.Enabled {
		if err := email.Enqueue(
			user.Email,
			"Подтверждение email",
			fmt.Sprintf("Перейдите по ссылке для подтверждения: %s/auth/verify?token=%s", s.cfg.Server.BaseURL, token),
		); err != nil {
			log.Error().Err(err).Str("email", user.Email).Msg("failed to enqueue verification email")
		}
	}
	return nil
}

func (s *EmailVerificationService) VerifyToken(ctx context.Context, tokenStr string) (*User, error) {
	token, err := s.emailVerifRepo.GetToken(ctx, tokenStr)
	if err != nil {
		return nil, errors.New("токен недействителен или истёк")
	}
	if token.ExpiresAt.Before(time.Now()) {
		return nil, errors.New("токен истёк")
	}
	if err := s.userRepo.Update(ctx, token.UserID, map[string]any{"email_verified": true}); err != nil {
		return nil, err
	}
	if err := s.emailVerifRepo.DeleteToken(ctx, token); err != nil {
		log.Warn().Err(err).Uint("user_id", token.UserID).Msg("не удалось удалить использованный токен подтверждения email")
	}
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

// GetDashboard собирает данные для дашборда с использованием реальных таблиц.
func (s *UserDashboardService) GetDashboard(userID uint) (*UserDashboard, error) {
	var dash UserDashboard

	// 1. Авторские игры (таблица games)
	var authoredGames []struct {
		ID      uint
		Name    string
		IsDraft bool
	}
	if err := s.DB.Table("games").
		Select("id, name, is_draft").
		Where("author_id = ?", userID).
		Find(&authoredGames).Error; err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("GetDashboard: failed to get authored games")
	} else {
		for _, g := range authoredGames {
			dash.AuthoredGames = append(dash.AuthoredGames, DashboardGame{
				ID:      g.ID,
				Name:    g.Name,
				IsDraft: g.IsDraft,
			})
		}
	}

	// 2. Все команды пользователя (капитанство + участие)
	var allTeams []DashboardTeam
	if err := s.DB.Raw(`
		SELECT id, name FROM teams WHERE captain_id = ?
		UNION
		SELECT t.id, t.name FROM teams t
		JOIN team_members tm ON tm.team_id = t.id
		WHERE tm.user_id = ? AND t.captain_id != ?
	`, userID, userID, userID).Scan(&allTeams).Error; err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("GetDashboard: failed to get teams")
		// Если ошибка, загружаем только приглашения и возвращаем
		s.loadInvitations(&dash, userID)
		return &dash, nil
	}

	if len(allTeams) == 0 {
		s.loadInvitations(&dash, userID)
		return &dash, nil
	}

	// 3. Прохождения для всех команд пользователя
	var passings []struct {
		ID     uint
		GameID uint
		TeamID uint
		Status string
	}
	teamIDs := make([]uint, len(allTeams))
	for i, t := range allTeams {
		teamIDs[i] = t.ID
	}

	if err := s.DB.Table("game_passings").
		Select("id, game_id, team_id, status").
		Where("team_id IN ? AND status IN (?, ?, ?)", teamIDs, "accepted", "started", "finished").
		Find(&passings).Error; err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("GetDashboard: failed to get passings")
		// Не фатально — продолжаем
	}

	// 4. Собираем ID игр и команд для подгрузки названий
	var gameIDs []uint
	var teamIDsForGames []uint
	for _, p := range passings {
		gameIDs = append(gameIDs, p.GameID)
		teamIDsForGames = append(teamIDsForGames, p.TeamID)
	}
	gameIDs = uniqueUintSlice(gameIDs)
	teamIDsForGames = uniqueUintSlice(teamIDsForGames)

	// 5. Подгружаем названия игр
	var gamesMap = make(map[uint]DashboardGame)
	if len(gameIDs) > 0 {
		var games []struct {
			ID   uint
			Name string
		}
		if err := s.DB.Table("games").Select("id, name").Where("id IN ?", gameIDs).Find(&games).Error; err == nil {
			for _, g := range games {
				gamesMap[g.ID] = DashboardGame{ID: g.ID, Name: g.Name}
			}
		}
	}

	// 6. Подгружаем названия команд
	var teamsMap = make(map[uint]DashboardTeam)
	if len(teamIDsForGames) > 0 {
		var teams []struct {
			ID   uint
			Name string
		}
		if err := s.DB.Table("teams").Select("id, name").Where("id IN ?", teamIDsForGames).Find(&teams).Error; err == nil {
			for _, t := range teams {
				teamsMap[t.ID] = DashboardTeam{ID: t.ID, Name: t.Name}
			}
		}
	}

	// 7. Определяем, в каких командах пользователь — капитан
	captainTeamIDs := make(map[uint]bool)
	for _, t := range allTeams {
		var captainID uint
		if err := s.DB.Table("teams").Select("captain_id").Where("id = ?", t.ID).Scan(&captainID).Error; err == nil {
			if captainID == userID {
				captainTeamIDs[t.ID] = true
			}
		}
	}

	// 8. Формируем результат
	for _, p := range passings {
		game, gameOk := gamesMap[p.GameID]
		team, teamOk := teamsMap[p.TeamID]
		if !gameOk || !teamOk {
			continue
		}
		if captainTeamIDs[p.TeamID] {
			dash.CaptainTeams = append(dash.CaptainTeams, DashboardTeamWithGame{
				Team: team,
				Game: game,
			})
		} else {
			dash.MemberTeams = append(dash.MemberTeams, DashboardTeamWithGame{
				Team: team,
				Game: game,
			})
		}
		if p.Status == "started" || p.Status == "accepted" {
			dash.ActivePassings = append(dash.ActivePassings, DashboardPassingWithGame{
				PassingStatus: p.Status,
				TeamName:      team.Name,
				GameName:      game.Name,
				GameID:        p.GameID,
				PassingID:     p.ID,
			})
		}
	}

	// 9. Приглашения
	s.loadInvitations(&dash, userID)

	return &dash, nil
}

// loadInvitations загружает ожидающие приглашения в структуру дашборда.
func (s *UserDashboardService) loadInvitations(dash *UserDashboard, userID uint) {
	var invitations []DashboardInvitation
	if err := s.DB.Table("invitations").
		Select("invitations.id, invitations.team_id, teams.name as team_name, invitations.status").
		Joins("JOIN teams ON teams.id = invitations.team_id").
		Where("invitations.user_id = ? AND invitations.status = ?", userID, "pending").
		Scan(&invitations).Error; err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("loadInvitations: failed to load invitations")
	} else {
		dash.PendingInvitations = invitations
	}
}

func uniqueUintSlice(input []uint) []uint {
	u := make([]uint, 0, len(input))
	m := make(map[uint]bool)
	for _, val := range input {
		if _, ok := m[val]; !ok {
			m[val] = true
			u = append(u, val)
		}
	}
	return u
}
