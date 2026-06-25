// internal/domain/user/handler.go
package user

import (
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
// @Summary Показать форму входа
// @Description Возвращает HTML-страницу с формой входа
// @Tags auth
// @Accept html
// @Produce html
// @Success 200 {string} html "Страница входа"
// @Router /auth/login [get]
func (h *AuthHandler) ShowLoginForm(c *gin.Context) {
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "auth-login.html",
		"csrf":         csrf.GetToken(c),
	})
}

// Login обрабатывает вход пользователя.
// @Summary Аутентификация пользователя
// @Description Вход в систему с получением JWT-токена (устанавливается в cookie)
// @Tags auth
// @Accept x-www-form-urlencoded
// @Produce html
// @Param email formData string true "Email пользователя"
// @Param password formData string true "Пароль"
// @Success 302 {string} string "Перенаправление на /dashboard"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Failure 401 {object} map[string]interface{} "Неверный email или пароль"
// @Router /auth/login [post]
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

	token, err := h.authSvc.Login(c.Request.Context(), input.Email, input.Password)
	if err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "auth-login.html",
			"Error":        "Неверный email или пароль",
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	userID, _ := h.authSvc.ParseToken(token)
	h.auditSvc.Log(userID, "login", "user", userID, input.Email)

	c.SetCookie("jwt", token, int(h.cfg.JWT.AccessExpiry.Seconds()), "/", "", false, true)
	c.Redirect(http.StatusFound, "/dashboard")
}

// Logout выполняет выход из системы, удаляя JWT-куку.
// @Summary Выход из системы
// @Description Удаляет JWT-куку и перенаправляет на главную
// @Tags auth
// @Produce html
// @Success 302 {string} string "Перенаправление на /"
// @Router /auth/logout [get]
func (h *AuthHandler) Logout(c *gin.Context) {
	c.SetCookie("jwt", "", -1, "/", "", false, true)
	c.Redirect(http.StatusFound, "/")
}

// ShowRegisterForm отображает страницу регистрации.
// @Summary Показать форму регистрации
// @Description Возвращает HTML-страницу с формой регистрации
// @Tags auth
// @Accept html
// @Produce html
// @Success 200 {string} html "Страница регистрации"
// @Router /auth/register [get]
func (h *AuthHandler) ShowRegisterForm(c *gin.Context) {
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "auth-register.html",
		"csrf":         csrf.GetToken(c),
	})
}

// Register обрабатывает регистрацию нового пользователя.
// @Summary Регистрация пользователя
// @Description Создаёт нового пользователя, отправляет письмо подтверждения и авторизует
// @Tags auth
// @Accept x-www-form-urlencoded
// @Produce html
// @Param email formData string true "Email пользователя"
// @Param password formData string true "Пароль (минимум 8 символов)"
// @Param name formData string true "Имя пользователя"
// @Success 302 {string} string "Перенаправление на /auth/login"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Failure 409 {object} map[string]interface{} "Email уже зарегистрирован"
// @Router /auth/register [post]
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

	user, err := h.authSvc.Register(c.Request.Context(), input.Email, input.Password, input.Name)
	if err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
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
// @Summary Показать форму восстановления пароля
// @Description Возвращает HTML-страницу для запроса сброса пароля
// @Tags auth
// @Accept html
// @Produce html
// @Success 200 {string} html "Страница восстановления"
// @Router /auth/forgot [get]
func (h *AuthHandler) ShowForgotForm(c *gin.Context) {
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "auth-forgot.html",
		"csrf":         csrf.GetToken(c),
	})
}

// ForgotPassword отправляет ссылку для сброса пароля.
// @Summary Запрос на сброс пароля
// @Description Отправляет на email ссылку для установки нового пароля
// @Tags auth
// @Accept x-www-form-urlencoded
// @Produce html
// @Param email formData string true "Email пользователя"
// @Success 200 {object} map[string]interface{} "Инструкция отправлена"
// @Failure 400 {object} map[string]interface{} "Некорректный email"
// @Router /auth/forgot [post]
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

	user, err := h.userService.GetByEmail(c.Request.Context(), input.Email)
	if err == nil {
		if _, err := h.passwordResetSvc.GenerateToken(c.Request.Context(), *user); err != nil {
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
// @Summary Показать форму сброса пароля
// @Description Возвращает HTML-страницу для ввода нового пароля по токену
// @Tags auth
// @Accept html
// @Produce html
// @Param token query string true "Токен сброса пароля"
// @Success 200 {string} html "Страница сброса пароля"
// @Router /auth/reset [get]
func (h *AuthHandler) ShowResetForm(c *gin.Context) {
	token := sanitize.StripHTML(c.Query("token"))
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "auth-reset.html",
		"Token":        token,
		"csrf":         csrf.GetToken(c),
	})
}

// ResetPassword устанавливает новый пароль по токену.
// @Summary Сброс пароля
// @Description Устанавливает новый пароль по токену, полученному по email
// @Tags auth
// @Accept x-www-form-urlencoded
// @Produce html
// @Param token formData string true "Токен сброса пароля"
// @Param password formData string true "Новый пароль (минимум 8 символов)"
// @Success 302 {string} string "Перенаправление на /auth/login"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации или неверный токен"
// @Router /auth/reset [post]
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

	if err := h.passwordResetSvc.ResetPassword(c.Request.Context(), input.Token, input.Password); err != nil {
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
// @Summary Подтверждение email
// @Description Активирует email пользователя по токену
// @Tags auth
// @Accept json
// @Produce html
// @Param token query string true "Токен подтверждения"
// @Success 200 {object} map[string]interface{} "Email подтверждён"
// @Failure 400 {object} map[string]interface{} "Неверный или истёкший токен"
// @Router /auth/verify [get]
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
// @Summary Начало OAuth-авторизации
// @Description Перенаправляет на страницу авторизации провайдера (Google, GitHub, Yandex)
// @Tags auth
// @Param provider path string true "Провайдер OAuth (google, github, yandex)"
// @Success 302 {string} string "Перенаправление на провайдера"
// @Failure 400 {object} map[string]interface{} "Неподдерживаемый провайдер"
// @Router /auth/oauth/{provider} [get]
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
// @Summary Обработка callback OAuth
// @Description Завершает OAuth-авторизацию, создаёт/входит пользователя
// @Tags auth
// @Param provider path string true "Провайдер OAuth"
// @Param code query string true "Код авторизации"
// @Param state query string true "Состояние (state)"
// @Success 302 {string} string "Перенаправление на /dashboard"
// @Failure 400 {object} map[string]interface{} "Ошибка авторизации"
// @Router /auth/oauth/{provider}/callback [get]
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
// @Summary Личный профиль
// @Description Отображает страницу профиля текущего пользователя
// @Tags profile
// @Produce html
// @Success 200 {string} html "Страница профиля"
// @Failure 404 {object} map[string]interface{} "Пользователь не найден"
// @Router /profile [get]
// @Security JWT
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
// @Summary Публичный профиль
// @Description Отображает публичный профиль пользователя по ID
// @Tags profile
// @Produce html
// @Param id path int true "ID пользователя"
// @Success 200 {string} html "Публичная страница профиля"
// @Failure 404 {object} map[string]interface{} "Пользователь не найден"
// @Failure 403 {object} map[string]interface{} "Профиль скрыт"
// @Router /profile/{id} [get]
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
		"CurrentUserID": c.GetUint("userID"),
	})
}

// UploadAvatar загружает аватар пользователя.
// @Summary Загрузка аватара
// @Description Загружает новый аватар текущего пользователя
// @Tags profile
// @Accept multipart/form-data
// @Produce html
// @Param avatar formData file true "Файл изображения (jpeg, png, webp)"
// @Success 302 {string} string "Перенаправление на /profile"
// @Failure 400 {object} map[string]interface{} "Ошибка загрузки"
// @Router /profile/avatar [post]
// @Security JWT
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

	if err := h.db.Model(&User{}).Where("id = ?", userID).Update("avatar_path", webPath).Error; err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("UploadAvatar: failed to update avatar_path")
	}
	c.Redirect(http.StatusFound, "/profile")
}

// UpdateProfile обновляет имя и email.
// @Summary Обновление профиля
// @Description Изменяет имя и email текущего пользователя
// @Tags profile
// @Accept x-www-form-urlencoded
// @Produce html
// @Param name formData string true "Новое имя"
// @Param email formData string true "Новый email"
// @Success 302 {string} string "Перенаправление на /profile"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Router /profile/update [post]
// @Security JWT
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

	if err := h.db.Model(&User{}).Where("id = ?", userID).Updates(map[string]any{
		"name":  input.Name,
		"email": input.Email,
	}).Error; err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("UpdateProfile: failed to update")
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "profile-show.html",
			"Error":        "Ошибка обновления профиля",
			"csrf":         csrf.GetToken(c),
		})
		return
	}
	c.Redirect(http.StatusFound, "/profile")
}

// ChangePassword меняет пароль пользователя.
// @Summary Смена пароля
// @Description Изменяет пароль текущего пользователя
// @Tags profile
// @Accept x-www-form-urlencoded
// @Produce html
// @Param old_password formData string true "Текущий пароль"
// @Param new_password formData string true "Новый пароль (минимум 8 символов)"
// @Success 302 {string} string "Перенаправление на /profile"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации или неверный пароль"
// @Router /profile/change-password [post]
// @Security JWT
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

	hashed, err := bcrypt.GenerateFromPassword([]byte(input.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "profile-show.html",
			"Error":        "Ошибка смены пароля",
			"csrf":         csrf.GetToken(c),
		})
		return
	}
	if err := h.db.Model(&user).Update("password", string(hashed)).Error; err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("ChangePassword: failed to update")
		c.HTML(http.StatusOK, "layout.html", gin.H{
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
// @Summary Список достижений
// @Description Отображает все достижения текущего пользователя
// @Tags achievements
// @Produce html
// @Success 200 {string} html "Страница достижений"
// @Router /achievements [get]
// @Security JWT
func (h *AchievementHandler) List(c *gin.Context) {
	userID := c.GetUint("userID")
	var achievements []Achievement
	h.db.Joins("JOIN user_achievements ON user_achievements.achievement_id = achievements.id").
		Where("user_achievements.user_id = ?", userID).
		Find(&achievements)
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
// @Summary Личный кабинет
// @Description Отображает дашборд текущего пользователя
// @Tags dashboard
// @Produce html
// @Success 200 {string} html "Страница дашборда"
// @Failure 500 {object} map[string]interface{} "Внутренняя ошибка"
// @Router /dashboard [get]
// @Security JWT
func (h *DashboardHandler) Index(c *gin.Context) {
	userID := c.GetUint("userID")
	dash, err := h.dashboardService.GetDashboard(userID)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "errors-500.html", nil)
		return
	}
	var role string
	h.db.Table("users").Select("role").Where("id = ?", userID).Scan(&role)
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock":  "dashboard-index.html",
		"Dashboard":     dash,
		"CurrentUserID": userID,
		"IsAdmin":       role == "admin",
	})
}
