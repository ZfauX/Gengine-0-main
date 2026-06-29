// internal/domain/user/handler.go
package user

import (
	"errors"
	"net/http"

	"gengine-0/internal/config"
	"gengine-0/internal/pkg/audit"
	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/sanitize"
	"gengine-0/internal/pkg/storage"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	csrf "github.com/utrack/gin-csrf"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// ---------- Входные структуры для валидации ----------

// UserIDRequest используется для валидации ID пользователя в URL.
type UserIDRequest struct {
	ID uint `uri:"id" binding:"required,gt=0"`
}

// OAuthProviderRequest используется для валидации провайдера в URL.
type OAuthProviderRequest struct {
	Provider string `uri:"provider" binding:"required,oneof=google github yandex"`
}

// VerifyEmailRequest используется для валидации токена в query.
type VerifyEmailRequest struct {
	Token string `form:"token" binding:"required"`
}

// RegisterInput – регистрация.
type RegisterInput struct {
	Email    string `form:"email" binding:"required,email"`
	Password string `form:"password" binding:"required,min=8,max=72"`
	Name     string `form:"name" binding:"required,min=2,max=50"`
}

// LoginInput – вход.
type LoginInput struct {
	Email    string `form:"email" binding:"required,email"`
	Password string `form:"password" binding:"required"`
}

// ForgotInput – восстановление пароля.
type ForgotInput struct {
	Email string `form:"email" binding:"required,email"`
}

// ResetInput – сброс пароля.
type ResetInput struct {
	Token    string `form:"token" binding:"required"`
	Password string `form:"password" binding:"required,min=8,max=72"`
}

// UpdateProfileInput – обновление профиля.
type UpdateProfileInput struct {
	Name  string `form:"name" binding:"required,min=2,max=50"`
	Email string `form:"email" binding:"required,email"`
}

// ChangePasswordInput – смена пароля.
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
	render.Page(c, http.StatusOK, "auth-login.html", gin.H{
		"csrf": csrf.GetToken(c),
	})
}

// Login обрабатывает вход пользователя.
func (h *AuthHandler) Login(c *gin.Context) {
	var input LoginInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "auth-login.html", gin.H{
			"Error": "Некорректные данные: " + err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	token, err := h.authSvc.Login(c.Request.Context(), input.Email, input.Password)
	if err != nil {
		render.Page(c, http.StatusUnauthorized, "auth-login.html", gin.H{
			"Error": "Неверный email или пароль",
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	userID, _, parseErr := h.authSvc.ParseToken(token)
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
	render.Page(c, http.StatusOK, "auth-register.html", gin.H{
		"csrf": csrf.GetToken(c),
	})
}

// Register обрабатывает регистрацию нового пользователя.
func (h *AuthHandler) Register(c *gin.Context) {
	var input RegisterInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "auth-register.html", gin.H{
			"Error": "Некорректные данные: " + err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	user, err := h.authSvc.Register(c.Request.Context(), input.Email, input.Password, input.Name)
	if err != nil {
		render.Page(c, http.StatusConflict, "auth-register.html", gin.H{
			"Error": "Email уже зарегистрирован",
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	h.auditSvc.Log(user.ID, "register", "user", user.ID, input.Email)
	c.Redirect(http.StatusFound, "/auth/login")
}

// ShowForgotForm отображает форму восстановления пароля.
func (h *AuthHandler) ShowForgotForm(c *gin.Context) {
	render.Page(c, http.StatusOK, "auth-forgot.html", gin.H{
		"csrf": csrf.GetToken(c),
	})
}

// ForgotPassword отправляет ссылку для сброса пароля.
func (h *AuthHandler) ForgotPassword(c *gin.Context) {
	var input ForgotInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "auth-forgot.html", gin.H{
			"Error": "Некорректный email",
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	user, err := h.userService.GetByEmail(c.Request.Context(), input.Email)
	if err != nil {
		log.Debug().Str("email", input.Email).Msg("ForgotPassword: user not found")
	} else {
		if _, err := h.passwordResetSvc.GenerateToken(c.Request.Context(), *user); err != nil {
			log.Error().Err(err).Str("email", input.Email).Msg("ForgotPassword: failed to generate token")
		}
	}

	render.Page(c, http.StatusOK, "auth-forgot.html", gin.H{
		"Message": "Инструкции отправлены на почту",
		"csrf":    csrf.GetToken(c),
	})
}

// ShowResetForm отображает форму установки нового пароля.
func (h *AuthHandler) ShowResetForm(c *gin.Context) {
	token := sanitize.StripHTML(c.Query("token"))
	render.Page(c, http.StatusOK, "auth-reset.html", gin.H{
		"Token": token,
		"csrf":  csrf.GetToken(c),
	})
}

// ResetPassword устанавливает новый пароль по токену.
func (h *AuthHandler) ResetPassword(c *gin.Context) {
	var input ResetInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "auth-reset.html", gin.H{
			"Token": c.PostForm("token"),
			"Error": "Некорректные данные: " + err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	if err := h.passwordResetSvc.ResetPassword(c.Request.Context(), input.Token, input.Password); err != nil {
		render.Page(c, http.StatusBadRequest, "auth-reset.html", gin.H{
			"Token": input.Token,
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}
	c.Redirect(http.StatusFound, "/auth/login")
}

// VerifyEmail подтверждает email пользователя.
func (h *AuthHandler) VerifyEmail(c *gin.Context) {
	var req VerifyEmailRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		render.Page(c, http.StatusBadRequest, "auth-verify_error.html", gin.H{
			"Error": "Неверный или отсутствующий токен",
		})
		return
	}

	if _, err := h.emailVerificationSvc.VerifyToken(c.Request.Context(), req.Token); err != nil {
		render.Page(c, http.StatusBadRequest, "auth-verify_error.html", gin.H{
			"Error": err.Error(),
		})
		return
	}
	render.Page(c, http.StatusOK, "auth-verify_success.html", gin.H{})
}

// OAuthLogin начинает процесс OAuth-авторизации.
func (h *AuthHandler) OAuthLogin(c *gin.Context) {
	var req OAuthProviderRequest
	if err := c.ShouldBindUri(&req); err != nil {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный провайдер"})
		return
	}

	url, err := h.oauthSvc.GetAuthURL(req.Provider)
	if err != nil {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, url)
}

// OAuthCallback обрабатывает обратный вызов OAuth-провайдера.
func (h *AuthHandler) OAuthCallback(c *gin.Context) {
	var req OAuthProviderRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.Page(c, http.StatusBadRequest, "auth-login.html", gin.H{
			"Error": "Неверный провайдер",
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	code := c.Query("code")
	state := c.Query("state")
	if code == "" {
		render.Page(c, http.StatusBadRequest, "auth-login.html", gin.H{
			"Error": "Отсутствует код авторизации",
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	user, err := h.oauthSvc.Authenticate(c.Request.Context(), req.Provider, code, state)
	if err != nil {
		render.Page(c, http.StatusBadRequest, "auth-login.html", gin.H{
			"Error": "Ошибка входа через " + req.Provider,
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	token, err := h.authSvc.GenerateJWT(*user)
	if err != nil {
		render.Page(c, http.StatusInternalServerError, "auth-login.html", gin.H{
			"Error": "Внутренняя ошибка",
			"csrf":  csrf.GetToken(c),
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
	render.Page(c, http.StatusOK, "profile-show.html", gin.H{
		"User":          user,
		"Achievements":  user.Achievements,
		"CurrentUserID": userID,
		"csrf":          csrf.GetToken(c),
	})
}

// PublicProfile отображает публичную страницу профиля.
func (h *ProfileHandler) PublicProfile(c *gin.Context) {
	var req UserIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID пользователя"})
		return
	}

	var user User
	if err := h.db.Preload("Achievements").First(&user, req.ID).Error; err != nil {
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
	render.Page(c, http.StatusOK, "profile-public.html", gin.H{
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

	// Валидация типа файла и размера уже есть в storage.Save, но можно добавить дополнительную проверку
	if header.Size > 2*1024*1024 {
		render.Page(c, http.StatusBadRequest, "profile-show.html", gin.H{
			"Error": "Размер файла не должен превышать 2 МБ",
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	allowedTypes := []string{"image/jpeg", "image/png", "image/webp"}
	webPath, err := h.storage.Save("uploads/avatars", file, header.Filename, userID, 2*1024*1024, allowedTypes)
	if err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("UploadAvatar: storage save failed")
		render.Page(c, http.StatusBadRequest, "profile-show.html", gin.H{
			"Error": "Ошибка загрузки аватара: " + err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	if err := h.db.Model(&User{}).Where("id = ?", userID).Update("avatar_path", webPath).Error; err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("UploadAvatar: failed to update avatar_path")
		render.Page(c, http.StatusInternalServerError, "profile-show.html", gin.H{
			"Error": "Не удалось сохранить путь к аватару",
			"csrf":  csrf.GetToken(c),
		})
		_ = h.storage.Delete(webPath)
		return
	}
	c.Redirect(http.StatusFound, "/profile")
}

// UpdateProfile обновляет имя и email.
func (h *ProfileHandler) UpdateProfile(c *gin.Context) {
	userID := c.GetUint("userID")

	var input UpdateProfileInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "profile-show.html", gin.H{
			"Error": "Некорректные данные: " + err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	if err := h.db.Model(&User{}).Where("id = ?", userID).Updates(map[string]any{
		"name":  input.Name,
		"email": input.Email,
	}).Error; err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("UpdateProfile: failed to update")
		render.Page(c, http.StatusInternalServerError, "profile-show.html", gin.H{
			"Error": "Ошибка обновления профиля",
			"csrf":  csrf.GetToken(c),
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
		render.Page(c, http.StatusBadRequest, "profile-show.html", gin.H{
			"Error": "Некорректные данные: " + err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	var user User
	if err := h.db.First(&user, userID).Error; err != nil {
		render.Page(c, http.StatusNotFound, "profile-show.html", gin.H{
			"Error": "Пользователь не найден",
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(input.OldPassword)); err != nil {
		render.Page(c, http.StatusBadRequest, "profile-show.html", gin.H{
			"Error": "Неверный текущий пароль",
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(input.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		render.Page(c, http.StatusInternalServerError, "profile-show.html", gin.H{
			"Error": "Ошибка смены пароля",
			"csrf":  csrf.GetToken(c),
		})
		return
	}
	if err := h.db.Model(&user).Update("password", string(hashed)).Error; err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("ChangePassword: failed to update")
		render.Page(c, http.StatusInternalServerError, "profile-show.html", gin.H{
			"Error": "Ошибка смены пароля",
			"csrf":  csrf.GetToken(c),
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
	render.Page(c, http.StatusOK, "achievements-list.html", gin.H{
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
	role, exists := c.Get("role")
	if !exists {
		role = "user"
	}
	isAdmin := false
	if roleStr, ok := role.(string); ok && roleStr == "admin" {
		isAdmin = true
	}
	render.Page(c, http.StatusOK, "dashboard-index.html", gin.H{
		"Dashboard":     dash,
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
	})
}
