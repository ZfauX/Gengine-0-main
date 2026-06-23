// internal/domain/user/service.go
package user

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
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

// AuthService содержит бизнес-логику аутентификации.
type AuthService struct {
	db  *gorm.DB
	cfg *config.Config
}

func NewAuthService(db *gorm.DB, cfg *config.Config) *AuthService {
	return &AuthService{db: db, cfg: cfg}
}

// Register создаёт нового пользователя и отправляет письмо подтверждения.
func (s *AuthService) Register(emailStr, password, name string) (*User, error) {
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	user := User{
		Email:    emailStr,
		Password: string(hashed),
		Name:     name,
	}
	if err := s.db.Create(&user).Error; err != nil {
		return nil, err
	}

	verificationService := NewEmailVerificationService(s.db, s.cfg)
	verificationService.SendVerificationEmail(user)

	return &user, nil
}

// Login проверяет учётные данные и возвращает JWT-токен.
func (s *AuthService) Login(emailStr, password string) (string, error) {
	var user User
	if err := s.db.Where("email = ?", emailStr).First(&user).Error; err != nil {
		return "", errors.New("неверный email или пароль")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		return "", errors.New("неверный email или пароль")
	}
	return s.generateJWT(user)
}

// GenerateJWT создаёт JWT-токен для пользователя (публичный, для OAuth).
func (s *AuthService) GenerateJWT(user User) (string, error) {
	return s.generateJWT(user)
}

// ParseToken проверяет JWT и возвращает ID пользователя.
func (s *AuthService) ParseToken(tokenStr string) (uint, error) {
	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("неверный метод подписи")
		}
		return []byte(s.cfg.JWT.Secret), nil
	})
	if err != nil || !token.Valid {
		return 0, errors.New("невалидный токен")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return 0, errors.New("неверные данные токена")
	}
	userIDFloat, ok := claims["user_id"].(float64)
	if !ok {
		return 0, errors.New("неверный ID пользователя в токене")
	}
	return uint(userIDFloat), nil
}

func (s *AuthService) generateJWT(user User) (string, error) {
	claims := jwt.MapClaims{
		"user_id": user.ID,
		"email":   user.Email,
		"exp":     time.Now().Add(s.cfg.JWT.AccessExpiry).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.cfg.JWT.Secret))
}

// ---------- UserService ----------

type UserService struct {
	db *gorm.DB
}

func NewUserService(db *gorm.DB) *UserService {
	return &UserService{db: db}
}

func (s *UserService) GetByID(id uint) (*User, error) {
	var user User
	if err := s.db.First(&user, id).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *UserService) GetByEmail(emailStr string) (*User, error) {
	var user User
	if err := s.db.Where("email = ?", emailStr).First(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *UserService) GetPublicProfile(id uint) (*User, error) {
	var user User
	if err := s.db.Preload("Achievements").First(&user, id).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *UserService) UpdateProfile(id uint, name, emailStr string) error {
	return s.db.Model(&User{}).Where("id = ?", id).Updates(map[string]any{
		"name":  name,
		"email": emailStr,
	}).Error
}

func (s *UserService) ChangePassword(id uint, oldPassword, newPassword string) error {
	var user User
	if err := s.db.First(&user, id).Error; err != nil {
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(oldPassword)); err != nil {
		return errors.New("неверный текущий пароль")
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return s.db.Model(&user).Update("password", string(hashed)).Error
}

// ---------- AchievementService ----------

type AchievementService struct {
	db *gorm.DB
}

func NewAchievementService(db *gorm.DB) *AchievementService {
	return &AchievementService{db: db}
}

func (s *AchievementService) AwardAchievement(userID uint, code string) error {
	var achievement Achievement
	if err := s.db.Where("code = ?", code).First(&achievement).Error; err != nil {
		return err
	}
	var count int64
	s.db.Table("user_achievements").Where("user_id = ? AND achievement_id = ?", userID, achievement.ID).Count(&count)
	if count > 0 {
		return nil
	}
	var user User
	if err := s.db.First(&user, userID).Error; err != nil {
		return err
	}
	return s.db.Model(&user).Association("Achievements").Append(&achievement)
}

func (s *AchievementService) GetUserAchievements(userID uint) ([]Achievement, error) {
	var achievements []Achievement
	err := s.db.Joins("JOIN user_achievements ON user_achievements.achievement_id = achievements.id").
		Where("user_achievements.user_id = ?", userID).
		Find(&achievements).Error
	return achievements, err
}

func (s *AchievementService) SeedAchievements() {
	achievements := []Achievement{
		{Code: "first_level_created", Name: "Первый уровень", Description: "Создайте свой первый уровень", Icon: "🏗️"},
		{Code: "five_games_hosted", Name: "Опытный организатор", Description: "Проведите 5 завершённых игр", Icon: "🎖️"},
		{Code: "hattrick", Name: "Хет-трик", Description: "Займите 1 место три раза подряд", Icon: "🏆"},
		{Code: "tactician", Name: "Тактик", Description: "Используйте подсказку и займите 1 место", Icon: "💡"},
		{Code: "collector", Name: "Коллекционер", Description: "Участвуйте в 10 завершённых играх", Icon: "🎮"},
		{Code: "speed_demon", Name: "Быстрый старт", Description: "Завершите игру менее чем за 5 минут", Icon: "⚡"},
	}
	for _, a := range achievements {
		if err := s.db.Where("code = ?", a.Code).FirstOrCreate(&a).Error; err != nil {
			log.Error().Err(err).Str("achievement", a.Code).Msg("SeedAchievements: failed to seed")
		}
	}
}

// ---------- OAuthService ----------

type OAuthService struct {
	db      *gorm.DB
	cfg     *config.Config
	configs map[string]*oauth2.Config
}

func NewOAuthService(db *gorm.DB, cfg *config.Config) *OAuthService {
	return &OAuthService{
		db:  db,
		cfg: cfg,
		configs: map[string]*oauth2.Config{
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
		},
	}
}

func (s *OAuthService) GetAuthURL(provider string) (string, error) {
	cfg, ok := s.configs[provider]
	if !ok {
		return "", errors.New("неподдерживаемый провайдер")
	}
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return "", fmt.Errorf("не удалось сгенерировать state: %w", err)
	}
	state := hex.EncodeToString(stateBytes)
	return cfg.AuthCodeURL(state, oauth2.AccessTypeOffline), nil
}

func (s *OAuthService) Authenticate(provider, code, state string) (*User, error) {
	if len(state) != 32 {
		return nil, errors.New("неверный state-параметр")
	}
	cfg, ok := s.configs[provider]
	if !ok {
		return nil, errors.New("неподдерживаемый провайдер")
	}
	_, err := cfg.Exchange(context.Background(), code)
	if err != nil {
		return nil, err
	}
	var emailStr string
	switch provider {
	case "google":
		emailStr = "user@gmail.com"
	case "github":
		emailStr = "user@github.com"
	case "yandex":
		emailStr = "user@yandex.ru"
	}
	var user User
	err = s.db.Where("email = ?", emailStr).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		user = User{
			Email:         emailStr,
			Name:          emailStr,
			EmailVerified: true,
			Password:      "",
		}
		s.db.Create(&user)
	} else if err != nil {
		return nil, err
	}
	return &user, nil
}

// ---------- PasswordResetService ----------

type PasswordResetService struct {
	db  *gorm.DB
	cfg *config.Config
}

func NewPasswordResetService(db *gorm.DB, cfg *config.Config) *PasswordResetService {
	return &PasswordResetService{db: db, cfg: cfg}
}

func (s *PasswordResetService) GenerateToken(user User) (string, error) {
	b := make([]byte, 16)
	rand.Read(b)
	rawToken := hex.EncodeToString(b)
	token := PasswordResetToken{
		UserID:    user.ID,
		Token:     rawToken,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	if err := s.db.Create(&token).Error; err != nil {
		return "", err
	}
	if s.cfg.SMTP.Enabled {
		emailService := email.NewEmailService(s.cfg)
		if err := emailService.Send(user.Email, "Сброс пароля",
			fmt.Sprintf("Для сброса пароля перейдите по ссылке: %s/auth/reset?token=%s", s.cfg.Server.BaseURL, rawToken)); err != nil {
			log.Error().Err(err).Str("email", user.Email).Msg("failed to send password reset email")
		}
	}
	return rawToken, nil
}

func (s *PasswordResetService) ResetPassword(tokenStr, newPassword string) error {
	var token PasswordResetToken
	if err := s.db.Where("token = ? AND expires_at > ?", tokenStr, time.Now()).First(&token).Error; err != nil {
		return errors.New("токен недействителен или истёк")
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := s.db.Model(&User{}).Where("id = ?", token.UserID).Update("password", string(hashed)).Error; err != nil {
		return err
	}
	s.db.Delete(&token)
	return nil
}

// ---------- EmailVerificationService ----------

type EmailVerificationService struct {
	db  *gorm.DB
	cfg *config.Config
}

func NewEmailVerificationService(db *gorm.DB, cfg *config.Config) *EmailVerificationService {
	return &EmailVerificationService{db: db, cfg: cfg}
}

func (s *EmailVerificationService) SendVerificationEmail(user User) {
	b := make([]byte, 16)
	rand.Read(b)
	token := hex.EncodeToString(b)
	s.db.Create(&EmailVerificationToken{
		UserID:    user.ID,
		Token:     token,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	})
	if s.cfg.SMTP.Enabled {
		emailService := email.NewEmailService(s.cfg)
		if err := emailService.Send(user.Email, "Подтверждение email",
			fmt.Sprintf("Перейдите по ссылке для подтверждения: %s/auth/verify?token=%s", s.cfg.Server.BaseURL, token)); err != nil {
			log.Error().Err(err).Str("email", user.Email).Msg("failed to send verification email")
		}
	}
}

func (s *EmailVerificationService) VerifyToken(tokenStr string) (*User, error) {
	var vt EmailVerificationToken
	if err := s.db.Where("token = ? AND expires_at > ?", tokenStr, time.Now()).First(&vt).Error; err != nil {
		return nil, errors.New("токен недействителен или истёк")
	}
	var user User
	if err := s.db.First(&user, vt.UserID).Error; err != nil {
		return nil, err
	}
	user.EmailVerified = true
	s.db.Save(&user)
	s.db.Delete(&vt)
	return &user, nil
}

// ---------- UserDashboardService ----------

// Локальные модели, заменяющие импорт game и team
type dashboardGame struct {
	ID        uint           `gorm:"primaryKey"`
	Name      string         `gorm:"column:name"`
	IsDraft   bool           `gorm:"column:is_draft"`
	AuthorID  uint           `gorm:"column:author_id"`
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

func (dashboardGame) TableName() string { return "games" }

type dashboardGamePassing struct {
	ID        uint           `gorm:"primaryKey"`
	GameID    uint           `gorm:"column:game_id"`
	TeamID    uint           `gorm:"column:team_id"`
	Status    string         `gorm:"column:status"`
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

func (dashboardGamePassing) TableName() string { return "game_passings" }

type dashboardTeam struct {
	ID        uint           `gorm:"primaryKey"`
	Name      string         `gorm:"column:name"`
	CaptainID uint           `gorm:"column:captain_id"`
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

func (dashboardTeam) TableName() string { return "teams" }

type dashboardInvitation struct {
	ID        uint           `gorm:"primaryKey"`
	TeamID    uint           `gorm:"column:team_id"`
	UserID    uint           `gorm:"column:user_id"`
	Status    string         `gorm:"column:status"`
	DeletedAt gorm.DeletedAt `gorm:"index"`
	Team      dashboardTeam  `gorm:"foreignKey:TeamID"`
}

func (dashboardInvitation) TableName() string { return "invitations" }

const statusPending = "pending"

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

func (s *UserDashboardService) GetDashboard(userID uint) (*UserDashboard, error) {
	var dash UserDashboard

	// Мои игры (авторство)
	var authoredGames []dashboardGame
	s.DB.Where("author_id = ?", userID).Find(&authoredGames)
	for _, g := range authoredGames {
		dash.AuthoredGames = append(dash.AuthoredGames, DashboardGame{
			ID:      g.ID,
			Name:    g.Name,
			IsDraft: g.IsDraft,
		})
	}

	// Команды, где я капитан
	var captainTeams []dashboardTeam
	s.DB.Where("captain_id = ?", userID).Find(&captainTeams)

	// Сразу добавляем все команды капитана в дашборд (даже без прохождений)
	for _, ct := range captainTeams {
		dash.CaptainTeams = append(dash.CaptainTeams, DashboardTeamWithGame{
			Team: DashboardTeam{ID: ct.ID, Name: ct.Name},
			Game: DashboardGame{}, // пустая игра
		})
	}

	// Команды, где я участник
	var memberTeamIDs []uint
	s.DB.Table("team_members").Where("user_id = ?", userID).Pluck("team_id", &memberTeamIDs)

	allTeamIDs := make([]uint, 0)
	for _, t := range captainTeams {
		allTeamIDs = append(allTeamIDs, t.ID)
	}
	allTeamIDs = append(allTeamIDs, memberTeamIDs...)
	allTeamIDs = uniqueUintSlice(allTeamIDs)

	// Прохождения для этих команд
	var passings []dashboardGamePassing
	if len(allTeamIDs) > 0 {
		s.DB.Where("team_id IN ? AND status IN (?, ?, ?)", allTeamIDs,
			"accepted", "started", "finished").
			Find(&passings)
	}

	// Заполняем информацию об играх для команд, у которых есть прохождения
	for _, p := range passings {
		var g dashboardGame
		s.DB.Where("id = ?", p.GameID).First(&g)
		var t dashboardTeam
		s.DB.Where("id = ?", p.TeamID).First(&t)

		// Обновляем запись для команды капитана
		for i, ct := range dash.CaptainTeams {
			if ct.Team.ID == p.TeamID {
				dash.CaptainTeams[i].Game = DashboardGame{ID: g.ID, Name: g.Name}
				break
			}
		}

		if contains(memberTeamIDs, p.TeamID) {
			dash.MemberTeams = append(dash.MemberTeams, DashboardTeamWithGame{
				Team: DashboardTeam{ID: t.ID, Name: t.Name},
				Game: DashboardGame{ID: g.ID, Name: g.Name},
			})
		}
		if p.Status == "started" || p.Status == "accepted" {
			dash.ActivePassings = append(dash.ActivePassings, DashboardPassingWithGame{
				PassingStatus: p.Status,
				TeamName:      t.Name,
				GameName:      g.Name,
				GameID:        p.GameID,
				PassingID:     p.ID,
			})
		}
	}

	// Приглашения
	var invitations []dashboardInvitation
	s.DB.Preload("Team").Where("user_id = ? AND status = ?", userID, statusPending).Find(&invitations)
	for _, inv := range invitations {
		dash.PendingInvitations = append(dash.PendingInvitations, DashboardInvitation{
			ID:       inv.ID,
			TeamID:   inv.TeamID,
			TeamName: inv.Team.Name,
			Status:   inv.Status,
		})
	}

	return &dash, nil
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

func contains(slice []uint, item uint) bool {
	return slices.Contains(slice, item)
}