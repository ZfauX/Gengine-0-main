// internal/domain/user/handler.go
package user

import (
	"errors"
	"net/http"
	"strconv"

	"gengine-0/internal/config"
	"gengine-0/internal/pkg/audit"
	"gengine-0/internal/pkg/sanitize"
	"gengine-0/internal/pkg/storage"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	csrf "github.com/utrack/gin-csrf"
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
	cfg                  *config.Config
	authSvc              *AuthService
	userService          *UserService
	passwordResetSvc     *PasswordResetService
	emailVerificationSvc *EmailVerificationService
	oauthSvc             *OAuthService
	auditSvc             *audit.Service
}

func NewAuthHandler(
	cfg *config.Config,
	authSvc *AuthService,
	userService *UserService,
	passwordResetSvc *PasswordResetService,
	emailVerificationSvc *EmailVerificationService,
	oauthSvc *OAuthService,
	auditSvc *audit.Service,
) *AuthHandler {
	return &AuthHandler{
		cfg:                  cfg,
		authSvc:              authSvc,
		userService:          userService,
		passwordResetSvc:     passwordResetSvc,
		emailVerificationSvc: emailVerificationSvc,
		oauthSvc:             oauthSvc,
		auditSvc:             auditSvc,
	}
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
		c.HTML(http.StatusBadRequest, "layout.html", gin.H{
			"ContentBlock": "auth-login.html",
			"Error":        "Некорректные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	token, err := h.authSvc.Login(c.Request.Context(), input.Email, input.Password)
	if err != nil {
		c.HTML(http.StatusUnauthorized, "layout.html", gin.H{
			"ContentBlock": "auth-login.html",
			"Error":        "Неверный email или пароль",
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	// Обработка ошибки парсинга токена для аудита — если ошибка, логируем, но не прерываем
	userID, parseErr := h.authSvc.ParseToken(token)
	if parseErr != nil {
		log.Error().Err(parseErr).Msg("Login: failed to parse token for audit")
	} else {
		h.auditSvc.Log(userID, "login", "user", userID, input.Email)
	}

	c.SetCookie("jwt", token, int(h.cfg.JWT.AccessExpiry.Seconds()), "/", "", false, true)
	c.Redirect(http.StatusFound, "/dashboard")
}

// Logout выполняет выход из системы.
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
		c.HTML(http.StatusBadRequest, "layout.html", gin.H{
			"ContentBlock": "auth-register.html",
			"Error":        "Некорректные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	user, err := h.authSvc.Register(c.Request.Context(), input.Email, input.Password, input.Name)
	if err != nil {
		c.HTML(http.StatusConflict, "layout.html", gin.H{
			"ContentBlock": "auth-register.html",
			"Error":        "Email уже зарегистрирован",
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	h.auditSvc.Log(user.ID, "register", "user", user.ID, input.Email)
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
		c.HTML(http.StatusBadRequest, "layout.html", gin.H{
			"ContentBlock": "auth-forgot.html",
			"Error":        "Некорректный email",
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	user, err := h.userService.GetByEmail(c.Request.Context(), input.Email)
	if err != nil {
		// Если пользователь не найден, не раскрываем информацию, просто показываем сообщение об успехе
		log.Debug().Str("email", input.Email).Msg("ForgotPassword: user not found")
	} else {
		if _, err := h.passwordResetSvc.GenerateToken(c.Request.Context(), *user); err != nil {
			// Ошибка генерации токена — логируем, но пользователю показываем общее сообщение
			log.Error().Err(err).Str("email", input.Email).Msg("ForgotPassword: failed to generate token")
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
	token := sanitize.StripHTML(c.Query("token"))
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
		c.HTML(http.StatusBadRequest, "layout.html", gin.H{
			"ContentBlock": "auth-reset.html",
			"Token":        c.PostForm("token"),
			"Error":        "Некорректные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	if err := h.passwordResetSvc.ResetPassword(c.Request.Context(), input.Token, input.Password); err != nil {
		c.HTML(http.StatusBadRequest, "layout.html", gin.H{
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
	if _, err := h.emailVerificationSvc.VerifyToken(c.Request.Context(), token); err != nil {
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
	url, err := h.oauthSvc.GetAuthURL(provider)
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

	user, err := h.oauthSvc.Authenticate(c.Request.Context(), provider, code, state)
	if err != nil {
		c.HTML(http.StatusBadRequest, "layout.html", gin.H{
			"ContentBlock": "auth-login.html",
			"Error":        "Ошибка входа через " + provider,
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	token, err := h.authSvc.GenerateJWT(*user)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "layout.html", gin.H{
			"ContentBlock": "auth-login.html",
			"Error":        "Внутренняя ошибка",
			"csrf":         csrf.GetToken(c),
		})
		return
	}
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
		"CurrentUserID": userID,
		"csrf":          csrf.GetToken(c),
	})
}

// PublicProfile отображает публичную страницу профиля.
func (h *ProfileHandler) PublicProfile(c *gin.Context) {
	idStr := c.Param("id")
	userID, err := strconv.Atoi(idStr)
	if err != nil {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID пользователя"})
		return
	}

	var user User
	if err := h.db.Preload("Achievements").First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.HTML(http.StatusNotFound, "errors/404.html", nil)
		} else {
			c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		}
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
		"CurrentUserID": c.GetUint("userID"),
	})
}

// UploadAvatar загружает аватар пользователя.
func (h *ProfileHandler) UploadAvatar(c *gin.Context) {
	userID := c.GetUint("userID")
	file, header, err := c.Request.FormFile("avatar")
	if err != nil {
		log.Warn().Err(err).Uint("user", userID).Msg("UploadAvatar: no file provided")
		c.Redirect(http.StatusFound, "/profile")
		return
	}
	defer func() { _ = file.Close() }()

	allowedTypes := []string{"image/jpeg", "image/png", "image/webp"}
	webPath, err := h.storage.Save("uploads/avatars", file, header.Filename, userID, 2*1024*1024, allowedTypes)
	if err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("UploadAvatar: storage save failed")
		c.HTML(http.StatusBadRequest, "layout.html", gin.H{
			"ContentBlock": "profile-show.html",
			"Error":        "Ошибка загрузки аватара: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	if err := h.db.Model(&User{}).Where("id = ?", userID).Update("avatar_path", webPath).Error; err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("UploadAvatar: failed to update avatar_path")
		c.HTML(http.StatusInternalServerError, "layout.html", gin.H{
			"ContentBlock": "profile-show.html",
			"Error":        "Не удалось сохранить путь к аватару",
			"csrf":         csrf.GetToken(c),
		})
		_ = h.storage.Delete(webPath) // пытаемся удалить уже загруженный файл
		return
	}
	c.Redirect(http.StatusFound, "/profile")
}

// UpdateProfile обновляет имя и email.
func (h *ProfileHandler) UpdateProfile(c *gin.Context) {
	userID := c.GetUint("userID")

	var input UpdateProfileInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusBadRequest, "layout.html", gin.H{
			"ContentBlock": "profile-show.html",
			"Error":        "Некорректные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	if err := h.db.Model(&User{}).Where("id = ?", userID).Updates(map[string]any{
		"name":  input.Name,
		"email": input.Email,
	}).Error; err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("UpdateProfile: failed to update")
		c.HTML(http.StatusInternalServerError, "layout.html", gin.H{
			"ContentBlock": "profile-show.html",
			"Error":        "Ошибка обновления профиля",
			"csrf":         csrf.GetToken(c),
		})
		return
	}
	c.Redirect(http.StatusFound, "/profile")
}

// ChangePassword меняет пароль пользователя.
func (h *ProfileHandler) ChangePassword(c *gin.Context) {
	userID := c.GetUint("userID")

	var input ChangePasswordInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusBadRequest, "layout.html", gin.H{
			"ContentBlock": "profile-show.html",
			"Error":        "Некорректные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	var user User
	if err := h.db.First(&user, userID).Error; err != nil {
		c.HTML(http.StatusNotFound, "layout.html", gin.H{
			"ContentBlock": "profile-show.html",
			"Error":        "Пользователь не найден",
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(input.OldPassword)); err != nil {
		c.HTML(http.StatusBadRequest, "layout.html", gin.H{
			"ContentBlock": "profile-show.html",
			"Error":        "Неверный текущий пароль",
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(input.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "layout.html", gin.H{
			"ContentBlock": "profile-show.html",
			"Error":        "Ошибка смены пароля",
			"csrf":         csrf.GetToken(c),
		})
		return
	}
	if err := h.db.Model(&user).Update("password", string(hashed)).Error; err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("ChangePassword: failed to update")
		c.HTML(http.StatusInternalServerError, "layout.html", gin.H{
			"ContentBlock": "profile-show.html",
			"Error":        "Ошибка смены пароля",
			"csrf":         csrf.GetToken(c),
		})
		return
	}
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
	if err := h.db.Joins("JOIN user_achievements ON user_achievements.achievement_id = achievements.id").
		Where("user_achievements.user_id = ?", userID).
		Find(&achievements).Error; err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("AchievementHandler.List: failed to fetch achievements")
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock":  "achievements-list.html",
		"Achievements":  achievements,
		"CurrentUserID": userID,
	})
}

// ---------- Дашборд ----------

type DashboardHandler struct {
	dashboardService *UserDashboardService
	db               *gorm.DB
}

func NewDashboardHandler(dashboardService *UserDashboardService, db *gorm.DB) *DashboardHandler {
	return &DashboardHandler{dashboardService: dashboardService, db: db}
}

// Index отображает личный кабинет.
func (h *DashboardHandler) Index(c *gin.Context) {
	userID := c.GetUint("userID")
	dash, err := h.dashboardService.GetDashboard(userID)
	if err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("DashboardHandler.Index: failed to get dashboard")
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}
	var role string
	if err := h.db.Table("users").Select("role").Where("id = ?", userID).Scan(&role).Error; err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("DashboardHandler.Index: failed to get role")
		role = "user" // fallback
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock":  "dashboard-index.html",
		"Dashboard":     dash,
		"CurrentUserID": userID,
		"IsAdmin":       role == "admin",
	})
}
