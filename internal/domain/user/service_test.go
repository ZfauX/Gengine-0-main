package user

import (
	"testing"
	"time"

	"gengine-0/internal/config"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func newTestDB() *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		panic(err)
	}
	if err := db.AutoMigrate(&User{}, &PasswordResetToken{}, &EmailVerificationToken{}); err != nil {
		panic(err)
	}
	return db
}

func newTestConfig() *config.Config {
	return &config.Config{
		JWT: config.JWTConfig{
			Secret:       "test-secret",
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

func TestRegister(t *testing.T) {
	db := newTestDB()
	cfg := newTestConfig()
	service := NewAuthService(db, cfg)

	user, err := service.Register("test@example.com", "password123", "Test User")
	assert.NoError(t, err)
	assert.NotZero(t, user.ID)
	assert.Equal(t, "test@example.com", user.Email)
	assert.True(t, bcrypt.CompareHashAndPassword([]byte(user.Password), []byte("password123")) == nil)
}

func TestLogin(t *testing.T) {
	db := newTestDB()
	cfg := newTestConfig()
	service := NewAuthService(db, cfg)

	_, err := service.Register("test@example.com", "password123", "Test User")
	assert.NoError(t, err)

	token, err := service.Login("test@example.com", "password123")
	assert.NoError(t, err)
	assert.NotEmpty(t, token)

	_, err = service.Login("test@example.com", "wrongpassword")
	assert.Error(t, err)
}

func TestParseToken(t *testing.T) {
	db := newTestDB()
	cfg := newTestConfig()
	service := NewAuthService(db, cfg)

	user, err := service.Register("test@example.com", "password123", "Test User")
	assert.NoError(t, err)

	token, err := service.Login("test@example.com", "password123")
	assert.NoError(t, err)

	parsedID, err := service.ParseToken(token)
	assert.NoError(t, err)
	assert.Equal(t, user.ID, parsedID)
}

func TestChangePassword(t *testing.T) {
	db := newTestDB()
	userService := NewUserService(db)

	hashed, _ := bcrypt.GenerateFromPassword([]byte("oldpassword"), bcrypt.DefaultCost)
	user := User{
		Email:    "test@example.com",
		Password: string(hashed),
		Name:     "Test User",
	}
	db.Create(&user)

	err := userService.ChangePassword(user.ID, "oldpassword", "newpassword")
	assert.NoError(t, err)

	var updated User
	db.First(&updated, user.ID)
	assert.True(t, bcrypt.CompareHashAndPassword([]byte(updated.Password), []byte("newpassword")) == nil)
}

func TestAwardAchievement(t *testing.T) {
	db := newTestDB()
	achievementService := NewAchievementService(db)
	achievementService.SeedAchievements()

	user := User{
		Email:    "test@example.com",
		Password: "password",
		Name:     "Test User",
	}
	db.Create(&user)

	err := achievementService.AwardAchievement(user.ID, "first_level_created")
	assert.NoError(t, err)

	err = achievementService.AwardAchievement(user.ID, "first_level_created")
	assert.NoError(t, err)

	achievements, _ := achievementService.GetUserAchievements(user.ID)
	assert.Len(t, achievements, 1)
}