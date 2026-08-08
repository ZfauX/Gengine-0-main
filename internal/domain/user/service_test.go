// internal/domain/user/service_test.go
package user

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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
			Secret:        "test-secret-secret-secret-secret",
			AccessExpiry:  24 * time.Hour,
			RefreshExpiry: 7 * 24 * time.Hour,
		},
		Server: config.ServerConfig{
			BaseURL: "http://localhost:8080",
		},
		SMTP: config.SMTPConfig{
			Enabled: false,
		},
		OAuth: config.OAuthConfig{
			Yandex: config.OAuthProvider{ClientID: "test", ClientSecret: "test"},
			VK:     config.OAuthProvider{ClientID: "test", ClientSecret: "test"},
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

// newTestAuthService собирает AuthService, связанный с RefreshTokenService
// (D2) — иначе refresh-методы возвращают «refresh-сервис не инициализирован».
func newTestAuthService(userRepo UserRepository, achievRepo AchievementRepository, emailVerifRepo EmailVerificationRepository, refreshTokenRepo RefreshTokenRepository, cfg *config.Config) *AuthService {
	svc := NewAuthService(userRepo, achievRepo, emailVerifRepo, refreshTokenRepo, cfg)
	refreshSvc := NewRefreshTokenService(refreshTokenRepo, userRepo, cfg, svc)
	return svc.WithRefreshService(refreshSvc)
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
	service := newTestAuthService(userRepo, achievRepo, emailVerifRepo, refreshTokenRepo, cfg)

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
	service := newTestAuthService(userRepo, achievRepo, emailVerifRepo, refreshTokenRepo, cfg)

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

// TestAuthService_LoginLockout: 5 неверных попыток блокируют аккаунт на 30 мин,
// generic-ответ не раскрывает причину (B2/lockout).
func TestAuthService_LoginLockout(t *testing.T) {
	db := newTestDB(t)
	cfg := newTestConfig()
	userRepo, achievRepo, _, emailVerifRepo, _, refreshTokenRepo := newTestRepos(db)
	service := newTestAuthService(userRepo, achievRepo, emailVerifRepo, refreshTokenRepo, cfg)

	user := createTestUser(t, db, "lock@example.com", "correctpass", "Lock")

	for i := 0; i < 5; i++ {
		_, err := service.Login(context.Background(), "lock@example.com", "wrongpass")
		require.Error(t, err)
		assert.Equal(t, "неверный email или пароль", err.Error())
	}

	// Аккаунт заблокирован: даже верный пароль не принимается, ответ generic.
	_, err := service.Login(context.Background(), "lock@example.com", "correctpass")
	assert.Equal(t, "неверный email или пароль", err.Error())

	// В БД выставлена блокировка.
	var stored User
	require.NoError(t, db.First(&stored, user.ID).Error)
	require.NotNil(t, stored.LockedUntil)
	assert.True(t, stored.LockedUntil.After(time.Now()))
}

// TestAuthService_RefreshRotation: старый refresh-токен не работает после ротации.
func TestAuthService_RefreshRotation(t *testing.T) {
	db := newTestDB(t)
	cfg := newTestConfig()
	userRepo, achievRepo, _, emailVerifRepo, _, refreshTokenRepo := newTestRepos(db)
	service := newTestAuthService(userRepo, achievRepo, emailVerifRepo, refreshTokenRepo, cfg)

	user := createTestUser(t, db, "rot@example.com", "pass", "Rot")

	r1, err := service.GenerateRefreshToken(context.Background(), *user, "dev1", "fp1")
	require.NoError(t, err)

	// Ротация: r1 → r2 (та же семья).
	_, r2, err := service.RefreshAccessToken(context.Background(), r1, "dev1", "fp1")
	require.NoError(t, err)
	require.NotEmpty(t, r2)

	// Старый токен отозван — повторное использование детектится как reuse
	// и отзывает всю семью (включая r2).
	_, _, err = service.RefreshAccessToken(context.Background(), r1, "dev1", "fp1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "отозваны")
}

// TestAuthService_ReuseRevokesFamily: повторное использование отозванного токена
// отзывает всю семью (включая уже выданные наследники).
func TestAuthService_ReuseRevokesFamily(t *testing.T) {
	db := newTestDB(t)
	cfg := newTestConfig()
	userRepo, achievRepo, _, emailVerifRepo, _, refreshTokenRepo := newTestRepos(db)
	service := newTestAuthService(userRepo, achievRepo, emailVerifRepo, refreshTokenRepo, cfg)

	user := createTestUser(t, db, "fam@example.com", "pass", "Fam")

	r1, err := service.GenerateRefreshToken(context.Background(), *user, "dev1", "fp1")
	require.NoError(t, err)

	// Ротация r1 → r2 (одна семья).
	_, r2, err := service.RefreshAccessToken(context.Background(), r1, "dev1", "fp1")
	require.NoError(t, err)

	// Повторное использование отозванного r1 → детект кражи, семья отзывается.
	_, _, err = service.RefreshAccessToken(context.Background(), r1, "dev1", "fp1")
	assert.Error(t, err)

	// r2 (наследник) тоже должен быть мёртв после отзыва семьи.
	_, _, err = service.RefreshAccessToken(context.Background(), r2, "dev1", "fp1")
	assert.Error(t, err)
}

// TestAuthService_FingerprintMismatch: неверный отпечаток клиента отклоняется,
// пустая строка не обходит привязку (S5).
func TestAuthService_FingerprintMismatch(t *testing.T) {
	db := newTestDB(t)
	cfg := newTestConfig()
	userRepo, achievRepo, _, emailVerifRepo, _, refreshTokenRepo := newTestRepos(db)
	service := newTestAuthService(userRepo, achievRepo, emailVerifRepo, refreshTokenRepo, cfg)

	user := createTestUser(t, db, "fp@example.com", "pass", "Fp")

	r1, err := service.GenerateRefreshToken(context.Background(), *user, "dev1", "fp1")
	require.NoError(t, err)

	// Неверный отпечаток.
	_, _, err = service.RefreshAccessToken(context.Background(), r1, "dev1", "fp2")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "отпечаток")

	// Пустой отпечаток НЕ обходит привязку (S5) — токен остаётся привязанным.
	_, _, err = service.RefreshAccessToken(context.Background(), r1, "dev1", "")
	assert.Error(t, err)
}

func TestAuthService_ParseToken(t *testing.T) {
	db := newTestDB(t)
	cfg := newTestConfig()
	userRepo, achievRepo, _, emailVerifRepo, _, refreshTokenRepo := newTestRepos(db)
	service := newTestAuthService(userRepo, achievRepo, emailVerifRepo, refreshTokenRepo, cfg)

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
	require.NoError(t, achievSvc.AwardAchievement(context.Background(), user.ID, "first_level_created"))

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
		require.NoError(t, service.AwardAchievement(context.Background(), user.ID, "five_games_hosted"))
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
		require.NoError(t, passResetRepo.CreateToken(context.Background(), expiredToken))
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
// Тесты для OAuthService
// =============================================================================

func TestOAuthService_GetAuthURL(t *testing.T) {
	cfg := newTestConfig()
	db := newTestDB(t)
	userRepo, _, _, _, extLoginRepo, _ := newTestRepos(db)
	service := NewOAuthService(userRepo, extLoginRepo, cfg)

	t.Run("поддерживаемый провайдер", func(t *testing.T) {
		url, state, err := service.GetAuthURL("yandex")
		require.NoError(t, err)
		assert.Contains(t, url, "oauth.yandex.com")
		assert.NotEmpty(t, state, "state должен быть сгенерирован")
		assert.Len(t, state, 32, "state должен иметь длину 32 символа")
	})

	t.Run("неподдерживаемый провайдер", func(t *testing.T) {
		_, _, err := service.GetAuthURL("facebook")
		assert.Error(t, err)
		assert.Equal(t, "неподдерживаемый провайдер", err.Error())
	})
}

// HIGH-5 (pass 29): Authenticate тестируется через httptest + кастомный
// RoundTripper, перенаправляющий запросы провайдеров на локальные серверы.
// Раньше тест был пустым t.Skip — security-critical путь без покрытия.
func TestOAuthService_Authenticate(t *testing.T) {
	cfg := newTestConfig()
	db := newTestDB(t)
	userRepo, _, _, _, extLoginRepo, _ := newTestRepos(db)
	service := NewOAuthService(userRepo, extLoginRepo, cfg)

	// httptest-серверы: token-эндпоинт + info-эндпоинт провайдера.
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// oauth2.Exchange ждёт access_token; для VK — ещё user_id и email.
		fmt.Fprintf(w, `{"access_token":"tok123","token_type":"Bearer","user_id":"vk42","email":"vk@example.com"}`)
	}))
	defer tokenServer.Close()

	infoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Ответ в формате Yandex info API.
		fmt.Fprintf(w, `{"id":"ya42","email":"ya@example.com","is_verified":true,"first_name":"Yandex","last_name":"User"}`)
	}))
	defer infoServer.Close()

	// Кастомный RoundTripper: token-запросы → tokenServer, info → infoServer.
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(r.URL.String(), "/token"):
			return tokenServer.Client().Transport.RoundTrip(rewriteURL(r, tokenServer.URL))
		case strings.Contains(r.URL.String(), "/info"), strings.Contains(r.URL.String(), "/method/users.get"):
			return infoServer.Client().Transport.RoundTrip(rewriteURL(r, infoServer.URL))
		default:
			return http.DefaultTransport.RoundTrip(r)
		}
	})
	service.httpClient = &http.Client{Transport: rt}
	// Перенаправляем token-эндпоинты провайдеров на тестовый сервер.
	service.configs["yandex"].Endpoint.TokenURL = tokenServer.URL + "/token"
	service.configs["vk"].Endpoint.TokenURL = tokenServer.URL + "/token"

	t.Run("новый пользователь через Yandex", func(t *testing.T) {
		u, err := service.Authenticate(context.Background(), "yandex", "code123", "state123")
		require.NoError(t, err)
		assert.Equal(t, "ya@example.com", u.Email)
		assert.True(t, u.EmailVerified)
		assert.Equal(t, "Yandex", u.Name)
		// Пользователь сохранён в БД.
		stored, err := userRepo.GetByEmail(context.Background(), "ya@example.com")
		require.NoError(t, err)
		assert.NotZero(t, stored.ID)
	})

	t.Run("новый пользователь через VK", func(t *testing.T) {
		u, err := service.Authenticate(context.Background(), "vk", "code456", "state123")
		require.NoError(t, err)
		assert.Equal(t, "vk@example.com", u.Email)
		assert.NotEmpty(t, u.Name)
	})

	t.Run("существующий пользователь не дублируется", func(t *testing.T) {
		_, err := service.Authenticate(context.Background(), "yandex", "code789", "state123")
		require.NoError(t, err)
		// Оба колбэка вернули одного пользователя.
		count, err := userRepo.Count(context.Background())
		require.NoError(t, err)
		assert.Equal(t, int64(2), count) // ya + vk
	})

	t.Run("пустой state отклоняется", func(t *testing.T) {
		_, err := service.Authenticate(context.Background(), "yandex", "code", "")
		assert.Error(t, err)
		assert.Equal(t, "неверный state-параметр", err.Error())
	})

	t.Run("неподдерживаемый провайдер", func(t *testing.T) {
		_, err := service.Authenticate(context.Background(), "facebook", "code", "state")
		assert.Error(t, err)
		assert.Equal(t, "неподдерживаемый провайдер", err.Error())
	})
}

// roundTripFunc — http.RoundTripper из функции.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// rewriteURL копирует запрос на другой origin (для тестового RoundTripper).
func rewriteURL(r *http.Request, base string) *http.Request {
	u := *r.URL
	parsed, err := url.Parse(base)
	if err != nil {
		return r
	}
	u.Scheme = parsed.Scheme
	u.Host = parsed.Host
	r2 := r.Clone(r.Context())
	r2.URL = &u
	return r2
}

// H4 (pass 30): extraString безопасно приводит oauth2.Extra-значения к строке.
func TestExtraString(t *testing.T) {
	tests := []struct {
		name string
		in   interface{}
		want string
	}{
		{"string", "vk42", "vk42"},
		{"float64", float64(123456), "123456"},
		{"json.Number", json.Number("123456"), "123456"},
		{"nil", nil, ""},
		{"bool", true, ""},
		{"float64 fractional", 12.5, "12.5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, extraString(tt.in))
		})
	}
}
