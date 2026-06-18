// internal/domain/user/handler.go
package user

import (
	"net/http"
	"strconv"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/pkg/storage"

	"github.com/rs/zerolog/log"
	"github.com/utrack/gin-csrf"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// ---------- Входные структуры для валидации ----------

type RegisterInput struct {
	Email    string `form:"email" binding:"required,email"`
	Password string `form:"password" binding:"required,min=8,max=72"`
	Name     string `form:"name" binding:"required,min=2,max=50"`
}

type LoginInput struct {
	Email    string `form:"email" binding:"required,email"`
	Password string `form:"password" binding:"required"`
}

type ForgotInput struct {
	Email string `form:"email" binding:"required,email"`
}

type ResetInput struct {
	Token    string `form:"token" binding:"required"`
	Password string `form:"password" binding:"required,min=8,max=72"`
}

type UpdateProfileInput struct {
	Name  string `form:"name" binding:"required,min=2,max=50"`
	Email string `form:"email" binding:"required,email"`
}

type ChangePasswordInput struct {
	OldPassword string `form:"old_password" binding:"required"`
	NewPassword string `form:"new_password" binding:"required,min=8,max=72"`
}

// ---------- Обработчики ----------

// AuthHandler обрабатывает аутентификацию и регистрацию.
type AuthHandler struct {
	db  *gorm.DB
	cfg *config.Config
}

func NewAuthHandler(db *gorm.DB, cfg *config.Config) *AuthHandler {
	return &AuthHandler{db: db, cfg: cfg}
}

// ShowLoginForm отображает страницу входа.
func (h *AuthHandler) ShowLoginForm(c *gin.Context) {
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "auth-login.html",
		"csrf":         csrf.GetToken(c),
	})
}

// Login обрабатывает вход пользователя.
func (h *AuthHandler) Login(c *gin.Context) {
	var input LoginInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "auth-login.html",
			"Error":        "Некорректные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	var user User
	if err := h.db.Where("email = ?", input.Email).First(&user).Error; err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "auth-login.html",
			"Error":        "Неверный email или пароль",
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(input.Password)); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "auth-login.html",
			"Error":        "Неверный email или пароль",
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	token, err := h.generateJWT(user)
	if err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "auth-login.html",
			"Error":        "Внутренняя ошибка",
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	c.SetCookie("jwt", token, int(h.cfg.JWT.AccessExpiry.Seconds()), "/", "", false, true)
	c.Redirect(http.StatusFound, "/dashboard")
}

// Logout выполняет выход из системы, удаляя JWT-куку.
func (h *AuthHandler) Logout(c *gin.Context) {
	c.SetCookie("jwt", "", -1, "/", "", false, true)
	c.Redirect(http.StatusFound, "/")
}

// ShowRegisterForm отображает страницу регистрации.
func (h *AuthHandler) ShowRegisterForm(c *gin.Context) {
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "auth-register.html",
		"csrf":         csrf.GetToken(c),
	})
}

// Register обрабатывает регистрацию нового пользователя.
func (h *AuthHandler) Register(c *gin.Context) {
	var input RegisterInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "auth-register.html",
			"Error":        "Некорректные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "auth-register.html",
			"Error":        "Ошибка создания пользователя",
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	user := User{
		Email:    input.Email,
		Password: string(hashedPassword),
		Name:     input.Name,
	}
	if err := h.db.Create(&user).Error; err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "auth-register.html",
			"Error":        "Email уже зарегистрирован",
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	if h.cfg.SMTP.Enabled && h.cfg.SMTP.Host != "" {
		emailVerificationService := NewEmailVerificationService(h.db, h.cfg)
		emailVerificationService.SendVerificationEmail(user)
	}

	c.Redirect(http.StatusFound, "/auth/login")
}

// ShowForgotForm отображает форму восстановления пароля.
func (h *AuthHandler) ShowForgotForm(c *gin.Context) {
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "auth-forgot.html",
		"csrf":         csrf.GetToken(c),
	})
}

// ForgotPassword отправляет ссылку для сброса пароля.
func (h *AuthHandler) ForgotPassword(c *gin.Context) {
	var input ForgotInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "auth-forgot.html",
			"Error":        "Некорректный email",
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	var user User
	if err := h.db.Where("email = ?", input.Email).First(&user).Error; err == nil {
		passwordResetService := NewPasswordResetService(h.db, h.cfg)
		if _, err := passwordResetService.GenerateToken(user); err != nil {
			log.Error().Err(err).Str("email", input.Email).Msg("failed to generate password reset token")
		}
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "auth-forgot.html",
		"Message":      "Инструкции отправлены на почту",
		"csrf":         csrf.GetToken(c),
	})
}

// ShowResetForm отображает форму установки нового пароля.
func (h *AuthHandler) ShowResetForm(c *gin.Context) {
	token := c.Query("token")
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "auth-reset.html",
		"Token":        token,
		"csrf":         csrf.GetToken(c),
	})
}

// ResetPassword устанавливает новый пароль по токену.
func (h *AuthHandler) ResetPassword(c *gin.Context) {
	var input ResetInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "auth-reset.html",
			"Token":        c.PostForm("token"),
			"Error":        "Некорректные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	passwordResetService := NewPasswordResetService(h.db, h.cfg)
	if err := passwordResetService.ResetPassword(input.Token, input.Password); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "auth-reset.html",
			"Token":        input.Token,
			"Error":        err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}
	c.Redirect(http.StatusFound, "/auth/login")
}

// VerifyEmail подтверждает email пользователя.
func (h *AuthHandler) VerifyEmail(c *gin.Context) {
	token := c.Query("token")
	emailVerificationService := NewEmailVerificationService(h.db, h.cfg)
	if _, err := emailVerificationService.VerifyToken(token); err != nil {
		c.HTML(http.StatusBadRequest, "layout.html", gin.H{
			"ContentBlock": "auth-verify_error.html",
			"Error":        err.Error(),
		})
		return
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "auth-verify_success.html",
	})
}

// OAuthLogin начинает процесс OAuth-авторизации.
func (h *AuthHandler) OAuthLogin(c *gin.Context) {
	provider := c.Param("provider")
	oauthService := NewOAuthService(h.db, h.cfg)
	url, err := oauthService.GetAuthURL(provider)
	if err != nil {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, url)
}

// OAuthCallback обрабатывает обратный вызов OAuth-провайдера.
func (h *AuthHandler) OAuthCallback(c *gin.Context) {
	provider := c.Param("provider")
	code := c.Query("code")
	state := c.Query("state")

	oauthService := NewOAuthService(h.db, h.cfg)
	user, err := oauthService.Authenticate(provider, code, state)
	if err != nil {
		c.HTML(http.StatusBadRequest, "layout.html", gin.H{
			"ContentBlock": "auth-login.html",
			"Error":        "Ошибка входа через " + provider,
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	token, _ := h.generateJWT(*user)
	c.SetCookie("jwt", token, int(h.cfg.JWT.AccessExpiry.Seconds()), "/", "", false, true)
	c.Redirect(http.StatusFound, "/dashboard")
}

// ---------- Профиль ----------

type ProfileHandler struct {
	db      *gorm.DB
	storage storage.FileStorage
}

func NewProfileHandler(db *gorm.DB, st storage.FileStorage) *ProfileHandler {
	return &ProfileHandler{db: db, storage: st}
}

// Show отображает личную страницу профиля.
func (h *ProfileHandler) Show(c *gin.Context) {
	userID := c.GetUint("userID")
	var user User
	if err := h.db.Preload("Achievements").First(&user, userID).Error; err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock":  "profile-show.html",
		"User":          user,
		"Achievements":  user.Achievements,
		"CurrentUserID": userID, // ← передаём ID для навигации
		"csrf":          csrf.GetToken(c),
	})
}

// PublicProfile отображает публичную страницу профиля.
func (h *ProfileHandler) PublicProfile(c *gin.Context) {
	userID, _ := strconv.Atoi(c.Param("id"))
	var user User
	if err := h.db.Preload("Achievements").First(&user, userID).Error; err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}
	if user.ProfileVisibility == "hidden" {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": "Профиль скрыт"})
		return
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock":  "profile-public.html",
		"ProfileUser":   user,
		"Achievements":  user.Achievements,
		"CurrentUserID": c.GetUint("userID"), // на случай, если зритель авторизован
	})
}

// UploadAvatar загружает аватар пользователя.
func (h *ProfileHandler) UploadAvatar(c *gin.Context) {
	userID := c.GetUint("userID")
	file, header, err := c.Request.FormFile("avatar")
	if err != nil {
		c.Redirect(http.StatusFound, "/profile")
		return
	}
	defer func() { _ = file.Close() }()

	allowedTypes := []string{"image/jpeg", "image/png", "image/webp"}
	webPath, err := h.storage.Save("uploads/avatars", file, header.Filename, userID, 2*1024*1024, allowedTypes)
	if err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "profile-show.html",
			"Error":        "Ошибка загрузки аватара",
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	h.db.Model(&User{}).Where("id = ?", userID).Update("avatar_path", webPath)
	c.Redirect(http.StatusFound, "/profile")
}

// UpdateProfile обновляет имя и email.
func (h *ProfileHandler) UpdateProfile(c *gin.Context) {
	userID := c.GetUint("userID")

	var input UpdateProfileInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "profile-show.html",
			"Error":        "Некорректные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	h.db.Model(&User{}).Where("id = ?", userID).Updates(map[string]interface{}{
		"name":  input.Name,
		"email": input.Email,
	})
	c.Redirect(http.StatusFound, "/profile")
}

// ChangePassword меняет пароль пользователя.
func (h *ProfileHandler) ChangePassword(c *gin.Context) {
	userID := c.GetUint("userID")

	var input ChangePasswordInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "profile-show.html",
			"Error":        "Некорректные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	var user User
	if err := h.db.First(&user, userID).Error; err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "profile-show.html",
			"Error":        "Пользователь не найден",
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(input.OldPassword)); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "profile-show.html",
			"Error":        "Неверный текущий пароль",
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	hashed, _ := bcrypt.GenerateFromPassword([]byte(input.NewPassword), bcrypt.DefaultCost)
	h.db.Model(&user).Update("password", string(hashed))
	c.Redirect(http.StatusFound, "/profile")
}

// ---------- Достижения ----------

type AchievementHandler struct {
	db *gorm.DB
}

func NewAchievementHandler(db *gorm.DB) *AchievementHandler {
	return &AchievementHandler{db: db}
}

// List отображает все достижения пользователя.
func (h *AchievementHandler) List(c *gin.Context) {
	userID := c.GetUint("userID")
	var achievements []Achievement
	h.db.Joins("JOIN user_achievements ON user_achievements.achievement_id = achievements.id").
		Where("user_achievements.user_id = ?", userID).
		Find(&achievements)
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock":  "achievements-list.html",
		"Achievements":  achievements,
		"CurrentUserID": userID, // ← для навигации
	})
}

// ---------- Дашборд ----------

type DashboardHandler struct {
	dashboardService *UserDashboardService
}

func NewDashboardHandler(dashboardService *UserDashboardService) *DashboardHandler {
	return &DashboardHandler{dashboardService: dashboardService}
}

// Index отображает личный кабинет.
func (h *DashboardHandler) Index(c *gin.Context) {
	userID := c.GetUint("userID")
	dash, err := h.dashboardService.GetDashboard(userID)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock":  "dashboard-index.html",
		"Dashboard":     dash,
		"CurrentUserID": userID, // ← теперь хидер будет как у авторизованного
	})
}

// ---------- Вспомогательные ----------

// generateJWT создаёт JWT-токен для пользователя.
func (h *AuthHandler) generateJWT(user User) (string, error) {
	claims := jwt.MapClaims{
		"user_id": user.ID,
		"email":   user.Email,
		"exp":     time.Now().Add(h.cfg.JWT.AccessExpiry).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(h.cfg.JWT.Secret))
}