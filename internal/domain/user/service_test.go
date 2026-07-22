// internal/domain/user/service_test.go
package user

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/pkg/crypto"
	"gengine-0/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// =============================================================================
// Вспомогательные функции для настройки тестов
// =============================================================================

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return testutil.SetupPostgresDB(t, &User{}, &Achievement{}, &PasswordResetToken{}, &EmailVerificationToken{}, &RefreshToken{})
}

func newTestConfig() *config.Config {
	return &config.Config{
		JWT: config.JWTConfig{
			Secret:       "test-secret-secret-secret-secret",
			AccessExpiry: 24 * time.Hour,
		},
		Server: config.ServerConfig{
			BaseURL: "http://localhost:8080",
		},
		SMTP: config.SMTPConfig{
			Enabled: false,
		},
		OAuth: config.OAuthConfig{
			Google: config.OAuthProvider{ClientID: "test", ClientSecret: "test"},
			GitHub: config.OAuthProvider{ClientID: "test", ClientSecret: "test"},
			Yandex: config.OAuthProvider{ClientID: "test", ClientSecret: "test"},
		},
	}
}

// Создаём все репозитории для тестов
func newTestRepos(db *gorm.DB) (
	UserRepository,
	AchievementRepository,
	PasswordResetRepository,
	EmailVerificationRepository,
	ExternalLoginRepository,
	RefreshTokenRepository,
) {
	return NewGormUserRepo(db),
		NewGormAchievementRepo(db),
		NewGormPasswordResetRepo(db),
		NewGormEmailVerificationRepo(db),
		NewGormExternalLoginRepo(db),
		NewGormRefreshTokenRepo(db)
}

// Создаёт тестового пользователя в БД
func createTestUser(t *testing.T, db *gorm.DB, email, password, name string) *User {
	t.Helper()
	hashed, _ := bcrypt.GenerateFromPassword([]byte(password), crypto.BcryptCost)
	user := &User{
		Email:         email,
		Password:      string(hashed),
		Name:          name,
		EmailVerified: false,
	}
	require.NoError(t, db.Create(user).Error)
	return user
}

// hashToken возвращает SHA-256 хеш строки токена (как в репозиториях).
func hashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

// =============================================================================
// Тесты для AuthService
// =============================================================================

func TestAuthService_Register(t *testing.T) {
	db := newTestDB(t)
	cfg := newTestConfig()
	userRepo, achievRepo, _, emailVerifRepo, _, refreshTokenRepo := newTestRepos(db)
	service := NewAuthService(userRepo, achievRepo, emailVerifRepo, refreshTokenRepo, cfg)

	t.Run("успешная регистрация", func(t *testing.T) {
		user, err := service.Register(context.Background(), "test@example.com", "password123", "Test User")
		require.NoError(t, err)
		assert.NotZero(t, user.ID)
		assert.Equal(t, "test@example.com", user.Email)
		assert.Equal(t, "Test User", user.Name)
		assert.NotEmpty(t, user.Password)
		// Проверяем, что пароль захэширован
		err = bcrypt.CompareHashAndPassword([]byte(user.Password), []byte("password123"))
		assert.NoError(t, err)
	})

	t.Run("регистрация с существующим email", func(t *testing.T) {
		// Создаём пользователя
		createTestUser(t, db, "duplicate@example.com", "pass", "Dupe")
		// Пытаемся зарегистрировать ещё одного
		_, err := service.Register(context.Background(), "duplicate@example.com", "newpass", "Another")
		assert.Error(t, err)
	})
}

func TestAuthService_Login(t *testing.T) {
	db := newTestDB(t)
	cfg := newTestConfig()
	userRepo, achievRepo, _, emailVerifRepo, _, refreshTokenRepo := newTestRepos(db)
	service := NewAuthService(userRepo, achievRepo, emailVerifRepo, refreshTokenRepo, cfg)

	// Создаём пользователя
	createTestUser(t, db, "login@example.com", "correctpass", "Login User")

	t.Run("успешный логин", func(t *testing.T) {
		token, err := service.Login(context.Background(), "login@example.com", "correctpass")
		require.NoError(t, err)
		assert.NotEmpty(t, token)
	})

	t.Run("неверный пароль", func(t *testing.T) {
		_, err := service.Login(context.Background(), "login@example.com", "wrongpass")
		assert.Error(t, err)
		assert.Equal(t, "неверный email или пароль", err.Error())
	})

	t.Run("неизвестный email", func(t *testing.T) {
		_, err := service.Login(context.Background(), "unknown@example.com", "anything")
		assert.Error(t, err)
		assert.Equal(t, "неверный email или пароль", err.Error())
	})
}

func TestAuthService_ParseToken(t *testing.T) {
	db := newTestDB(t)
	cfg := newTestConfig()
	userRepo, achievRepo, _, emailVerifRepo, _, refreshTokenRepo := newTestRepos(db)
	service := NewAuthService(userRepo, achievRepo, emailVerifRepo, refreshTokenRepo, cfg)

	user := createTestUser(t, db, "parse@example.com", "pass", "Parse")
	tokenStr, err := service.GenerateJWT(*user)
	require.NoError(t, err)

	t.Run("валидный токен", func(t *testing.T) {
		id, role, err := service.ParseToken(tokenStr)
		require.NoError(t, err)
		assert.Equal(t, user.ID, id)
		assert.Equal(t, "user", role)
	})

	t.Run("невалидный токен", func(t *testing.T) {
		_, _, err := service.ParseToken("invalid.token.string")
		assert.Error(t, err)
	})

	t.Run("просроченный токен", func(t *testing.T) {
		// Создаём просроченный токен вручную
		oldCfg := *cfg
		oldCfg.JWT.AccessExpiry = -time.Hour
		expiredService := NewAuthService(userRepo, achievRepo, emailVerifRepo, refreshTokenRepo, &oldCfg)
		token, _ := expiredService.GenerateJWT(*user)
		_, _, err := expiredService.ParseToken(token)
		assert.Error(t, err)
	})
}

// =============================================================================
// Тесты для UserService
// =============================================================================

func TestUserService_GetByID(t *testing.T) {
	db := newTestDB(t)
	userRepo, _, _, _, _, _ := newTestRepos(db)
	service := NewUserService(userRepo)

	user := createTestUser(t, db, "getbyid@example.com", "pass", "GetByID")

	t.Run("пользователь найден", func(t *testing.T) {
		found, err := service.GetByID(context.Background(), user.ID)
		require.NoError(t, err)
		assert.Equal(t, user.Email, found.Email)
	})

	t.Run("пользователь не найден", func(t *testing.T) {
		_, err := service.GetByID(context.Background(), 99999)
		assert.Error(t, err)
	})
}

func TestUserService_GetByEmail(t *testing.T) {
	db := newTestDB(t)
	userRepo, _, _, _, _, _ := newTestRepos(db)
	service := NewUserService(userRepo)

	user := createTestUser(t, db, "getbyemail@example.com", "pass", "GetByEmail")

	t.Run("пользователь найден", func(t *testing.T) {
		found, err := service.GetByEmail(context.Background(), "getbyemail@example.com")
		require.NoError(t, err)
		assert.Equal(t, user.ID, found.ID)
	})

	t.Run("пользователь не найден", func(t *testing.T) {
		_, err := service.GetByEmail(context.Background(), "nonexistent@example.com")
		assert.Error(t, err)
	})
}

func TestUserService_GetPublicProfile(t *testing.T) {
	db := newTestDB(t)
	userRepo, _, _, _, _, _ := newTestRepos(db)
	service := NewUserService(userRepo)

	user := createTestUser(t, db, "public@example.com", "pass", "Public")
	// Добавляем достижение (для проверки прелоада)
	achievRepo := NewGormAchievementRepo(db)
	achievSvc := NewAchievementService(achievRepo)
	achievSvc.SeedAchievements(context.Background())
	_ = achievSvc.AwardAchievement(context.Background(), user.ID, "first_level_created")

	t.Run("профиль с достижениями", func(t *testing.T) {
		profile, err := service.GetPublicProfile(context.Background(), user.ID)
		require.NoError(t, err)
		assert.Equal(t, user.Name, profile.Name)
		// Проверяем, что достижения подгружены (если есть)
		assert.NotNil(t, profile.Achievements)
	})
}

func TestUserService_UpdateProfile(t *testing.T) {
	db := newTestDB(t)
	userRepo, _, _, _, _, _ := newTestRepos(db)
	service := NewUserService(userRepo)

	user := createTestUser(t, db, "update@example.com", "pass", "Old Name")

	err := service.UpdateProfile(context.Background(), user.ID, "New Name", "newemail@example.com")
	require.NoError(t, err)

	updated, _ := service.GetByID(context.Background(), user.ID)
	assert.Equal(t, "New Name", updated.Name)
	assert.Equal(t, "newemail@example.com", updated.Email)
}

func TestUserService_ChangePassword(t *testing.T) {
	db := newTestDB(t)
	userRepo, _, _, _, _, _ := newTestRepos(db)
	service := NewUserService(userRepo)

	user := createTestUser(t, db, "changepass@example.com", "oldpass", "Change")

	t.Run("успешная смена", func(t *testing.T) {
		err := service.ChangePassword(context.Background(), user.ID, "oldpass", "newpass")
		require.NoError(t, err)

		updated, _ := service.GetByID(context.Background(), user.ID)
		// Проверяем, что хеш изменился
		err = bcrypt.CompareHashAndPassword([]byte(updated.Password), []byte("newpass"))
		assert.NoError(t, err)
	})

	t.Run("неверный старый пароль", func(t *testing.T) {
		err := service.ChangePassword(context.Background(), user.ID, "wrongold", "anything")
		assert.Error(t, err)
		assert.Equal(t, "неверный текущий пароль", err.Error())
	})
}

// =============================================================================
// Тесты для AchievementService
// =============================================================================

func TestAchievementService_AwardAndGet(t *testing.T) {
	db := newTestDB(t)
	_, achievRepo, _, _, _, _ := newTestRepos(db)
	service := NewAchievementService(achievRepo)
	service.SeedAchievements(context.Background())

	user := createTestUser(t, db, "achiev@example.com", "pass", "Achiever")

	t.Run("выдача достижения", func(t *testing.T) {
		err := service.AwardAchievement(context.Background(), user.ID, "first_level_created")
		require.NoError(t, err)
	})

	t.Run("повторная выдача того же достижения не создаёт дубликат", func(t *testing.T) {
		err := service.AwardAchievement(context.Background(), user.ID, "first_level_created")
		require.NoError(t, err)
		achievements, _ := service.GetUserAchievements(context.Background(), user.ID)
		assert.Len(t, achievements, 1) // должно быть только одно
	})

	t.Run("получение всех достижений пользователя", func(t *testing.T) {
		_ = service.AwardAchievement(context.Background(), user.ID, "five_games_hosted")
		achievements, err := service.GetUserAchievements(context.Background(), user.ID)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(achievements), 2)
	})
}

// =============================================================================
// Тесты для PasswordResetService
// =============================================================================

func TestPasswordResetService_GenerateToken(t *testing.T) {
	db := newTestDB(t)
	cfg := newTestConfig()
	userRepo, _, passResetRepo, _, _, _ := newTestRepos(db)
	service := NewPasswordResetService(userRepo, passResetRepo, cfg)

	user := createTestUser(t, db, "reset@example.com", "pass", "Reset")

	resetCode, err := service.GenerateToken(context.Background(), *user)
	require.NoError(t, err)
	assert.NotEmpty(t, resetCode)

	// Проверяем, что токен сохранён в БД (ищем по reset_code)
	var stored PasswordResetToken
	err = db.Where("reset_code = ?", resetCode).First(&stored).Error
	require.NoError(t, err)
	assert.Equal(t, user.ID, stored.UserID)
	assert.True(t, stored.ExpiresAt.After(time.Now()))
}

func TestPasswordResetService_ResetPassword(t *testing.T) {
	db := newTestDB(t)
	cfg := newTestConfig()
	userRepo, _, passResetRepo, _, _, _ := newTestRepos(db)
	service := NewPasswordResetService(userRepo, passResetRepo, cfg)

	user := createTestUser(t, db, "reset2@example.com", "oldpass", "Reset2")

	// Генерируем токен
	resetCode, err := service.GenerateToken(context.Background(), *user)
	require.NoError(t, err)

	t.Run("успешный сброс пароля", func(t *testing.T) {
		err := service.ResetPassword(context.Background(), resetCode, "newpass")
		require.NoError(t, err)

		updated, _ := userRepo.GetByID(context.Background(), user.ID)
		err = bcrypt.CompareHashAndPassword([]byte(updated.Password), []byte("newpass"))
		assert.NoError(t, err)

		// ResetCode должен быть удалён
		_, err = passResetRepo.GetTokenByResetCode(context.Background(), resetCode)
		assert.Error(t, err)
	})

	t.Run("попытка сброса с истекшим токеном", func(t *testing.T) {
		// Создаём просроченный токен вручную
		expiredToken := &PasswordResetToken{
			UserID:    user.ID,
			ResetCode: "expired-code-123",
			TokenHash: hashToken("expiredtoken"),
			ExpiresAt: time.Now().Add(-time.Hour),
		}
		_ = passResetRepo.CreateToken(context.Background(), expiredToken)
		err := service.ResetPassword(context.Background(), "expired-code-123", "any")
		assert.Error(t, err)
		assert.Equal(t, "токен истёк", err.Error())
	})

	t.Run("несуществующий код", func(t *testing.T) {
		err := service.ResetPassword(context.Background(), "nonexistent-code", "any")
		assert.Error(t, err)
		assert.Equal(t, "токен недействителен или истёк", err.Error())
	})
}

// =============================================================================
// Тесты для EmailVerificationService
// =============================================================================

func TestEmailVerificationService_VerifyToken(t *testing.T) {
	db := newTestDB(t)
	cfg := newTestConfig()
	userRepo, _, _, emailVerifRepo, _, _ := newTestRepos(db)
	service := NewEmailVerificationService(userRepo, emailVerifRepo, cfg)

	user := createTestUser(t, db, "verify@example.com", "pass", "Verify")

	// Создаём токен вручную (в реальности он создаётся при регистрации)
	token := &EmailVerificationToken{
		UserID:    user.ID,
		TokenHash: hashToken("validtoken"),
		ExpiresAt: time.Now().Add(time.Hour),
	}
	_ = emailVerifRepo.CreateToken(context.Background(), token)

	t.Run("успешная верификация", func(t *testing.T) {
		verifiedUser, err := service.VerifyToken(context.Background(), "validtoken")
		require.NoError(t, err)
		assert.True(t, verifiedUser.EmailVerified)
		// Токен должен быть удалён
		_, err = emailVerifRepo.GetToken(context.Background(), "validtoken")
		assert.Error(t, err)
	})

	t.Run("истекший токен", func(t *testing.T) {
		expired := &EmailVerificationToken{
			UserID:    user.ID,
			TokenHash: hashToken("expiredverif"),
			ExpiresAt: time.Now().Add(-time.Hour),
		}
		_ = emailVerifRepo.CreateToken(context.Background(), expired)
		_, err := service.VerifyToken(context.Background(), "expiredverif")
		assert.Error(t, err)
		assert.Equal(t, "токен истёк", err.Error())
	})

	t.Run("недействительный токен", func(t *testing.T) {
		_, err := service.VerifyToken(context.Background(), "nonexistent")
		assert.Error(t, err)
		assert.Equal(t, "токен недействителен или истёк", err.Error())
	})
}

// =============================================================================
// Тесты для OAuthService
// =============================================================================

func TestOAuthService_GetAuthURL(t *testing.T) {
	cfg := newTestConfig()
	db := newTestDB(t)
	userRepo, _, _, _, extLoginRepo, _ := newTestRepos(db)
	service := NewOAuthService(userRepo, extLoginRepo, cfg)

	t.Run("поддерживаемый провайдер", func(t *testing.T) {
		url, state, err := service.GetAuthURL("google")
		require.NoError(t, err)
		assert.Contains(t, url, "accounts.google.com")
		assert.NotEmpty(t, state, "state должен быть сгенерирован")
		assert.Len(t, state, 32, "state должен иметь длину 32 символа")
	})

	t.Run("неподдерживаемый провайдер", func(t *testing.T) {
		_, _, err := service.GetAuthURL("facebook")
		assert.Error(t, err)
		assert.Equal(t, "неподдерживаемый провайдер", err.Error())
	})
}

// Тест Authenticate требует реального обмена кодами, поэтому пропускаем его.
func TestOAuthService_Authenticate(t *testing.T) {
	t.Skip("Для выполнения требуется реальный OAuth-сервер, используйте интеграционные тесты")
}
