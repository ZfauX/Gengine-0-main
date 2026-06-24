// internal/domain/user/service_test.go
package user

import (
	"context"
	"testing"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return testutil.SetupPostgresDB(t, &User{}, &Achievement{}, &PasswordResetToken{}, &EmailVerificationToken{})
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
	}
}

// Создаём репозитории для тестов
func newTestRepos(db *gorm.DB) (UserRepository, AchievementRepository, EmailVerificationRepository) {
	return NewGormUserRepo(db), NewGormAchievementRepo(db), NewGormEmailVerificationRepo(db)
}

func TestRegister(t *testing.T) {
	db := newTestDB(t)
	cfg := newTestConfig()
	userRepo, achievRepo, emailVerifRepo := newTestRepos(db)
	service := NewAuthService(userRepo, achievRepo, emailVerifRepo, cfg)

	user, err := service.Register(context.Background(), "test@example.com", "password123", "Test User")
	require.NoError(t, err)
	assert.NotZero(t, user.ID)
	assert.Equal(t, "test@example.com", user.Email)
	assert.True(t, bcrypt.CompareHashAndPassword([]byte(user.Password), []byte("password123")) == nil)
}

func TestLogin(t *testing.T) {
	db := newTestDB(t)
	cfg := newTestConfig()
	userRepo, achievRepo, emailVerifRepo := newTestRepos(db)
	service := NewAuthService(userRepo, achievRepo, emailVerifRepo, cfg)

	_, err := service.Register(context.Background(), "test@example.com", "password123", "Test User")
	require.NoError(t, err)

	token, err := service.Login(context.Background(), "test@example.com", "password123")
	require.NoError(t, err)
	assert.NotEmpty(t, token)

	_, err = service.Login(context.Background(), "test@example.com", "wrongpassword")
	assert.Error(t, err)
}

func TestParseToken(t *testing.T) {
	db := newTestDB(t)
	cfg := newTestConfig()
	userRepo, achievRepo, emailVerifRepo := newTestRepos(db)
	service := NewAuthService(userRepo, achievRepo, emailVerifRepo, cfg)

	user, err := service.Register(context.Background(), "test@example.com", "password123", "Test User")
	require.NoError(t, err)

	token, err := service.Login(context.Background(), "test@example.com", "password123")
	require.NoError(t, err)

	parsedID, err := service.ParseToken(token)
	require.NoError(t, err)
	assert.Equal(t, user.ID, parsedID)
}

func TestChangePassword(t *testing.T) {
	db := newTestDB(t)
	userRepo, _, _ := newTestRepos(db)
	userService := NewUserService(userRepo)

	hashed, _ := bcrypt.GenerateFromPassword([]byte("oldpassword"), bcrypt.DefaultCost)
	user := User{
		Email:    "test@example.com",
		Password: string(hashed),
		Name:     "Test User",
	}
	require.NoError(t, db.Create(&user).Error)

	err := userService.ChangePassword(context.Background(), user.ID, "oldpassword", "newpassword")
	require.NoError(t, err)

	var updated User
	db.First(&updated, user.ID)
	assert.True(t, bcrypt.CompareHashAndPassword([]byte(updated.Password), []byte("newpassword")) == nil)
}

func TestAwardAchievement(t *testing.T) {
	db := newTestDB(t)
	_, achievRepo, _ := newTestRepos(db)
	achievementService := NewAchievementService(achievRepo)
	achievementService.SeedAchievements(context.Background())

	user := User{
		Email:    "test@example.com",
		Password: "password",
		Name:     "Test User",
	}
	require.NoError(t, db.Create(&user).Error)

	err := achievementService.AwardAchievement(context.Background(), user.ID, "first_level_created")
	require.NoError(t, err)

	err = achievementService.AwardAchievement(context.Background(), user.ID, "first_level_created")
	require.NoError(t, err)

	achievements, _ := achievementService.GetUserAchievements(context.Background(), user.ID)
	assert.Len(t, achievements, 1)
}
