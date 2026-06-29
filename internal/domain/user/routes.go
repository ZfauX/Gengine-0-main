// internal/domain/user/routes.go
package user

import (
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/pkg/audit"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterRoutes регистрирует все маршруты пользовательского домена.
// @tags auth
// @tags profile
// @tags achievements
// @tags dashboard
func RegisterRoutes(
	r *gin.Engine,
	cfg *config.Config,
	authSvc *AuthService,
	userSvc *UserService,
	passwordResetSvc *PasswordResetService,
	emailVerifSvc *EmailVerificationService,
	oauthSvc *OAuthService,
	auditSvc *audit.Service,
	db *gorm.DB,
	localStorage storage.FileStorage,
) {
	authHandler := NewAuthHandler(cfg, authSvc, userSvc, passwordResetSvc, emailVerifSvc, oauthSvc, auditSvc)
	profileHandler := NewProfileHandler(db, localStorage)
	achievementHandler := NewAchievementHandler(db)
	dashboardHandler := NewDashboardHandler(NewUserDashboardService(db), db)

	oauthRateLimit := middleware.LoginRateLimit(5*time.Minute, 5)

	authGroup := r.Group("/auth")
	{
		// @Summary Показать форму входа
		// @Description Возвращает HTML-страницу с формой входа
		// @Tags auth
		// @Accept html
		// @Produce html
		// @Success 200 {string} html "Страница входа"
		// @Router /auth/login [get]
		authGroup.GET("/login", authHandler.ShowLoginForm)

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
		authGroup.POST("/login", middleware.LoginRateLimit(5*time.Minute, 5), authHandler.Login)

		// @Summary Показать форму регистрации
		// @Description Возвращает HTML-страницу с формой регистрации
		// @Tags auth
		// @Accept html
		// @Produce html
		// @Success 200 {string} html "Страница регистрации"
		// @Router /auth/register [get]
		authGroup.GET("/register", authHandler.ShowRegisterForm)

		// @Summary Регистрация пользователя
		// @Description Создаёт нового пользователя
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
		authGroup.POST("/register", authHandler.Register)

		// @Summary Выход из системы
		// @Description Удаляет JWT-куку и перенаправляет на главную
		// @Tags auth
		// @Produce html
		// @Success 302 {string} string "Перенаправление на /"
		// @Router /auth/logout [get]
		authGroup.GET("/logout", authHandler.Logout)

		// @Summary Показать форму восстановления пароля
		// @Description Возвращает HTML-страницу для запроса сброса пароля
		// @Tags auth
		// @Accept html
		// @Produce html
		// @Success 200 {string} html "Страница восстановления"
		// @Router /auth/forgot [get]
		authGroup.GET("/forgot", authHandler.ShowForgotForm)

		// @Summary Запрос на сброс пароля
		// @Description Отправляет на email ссылку для установки нового пароля
		// @Tags auth
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param email formData string true "Email пользователя"
		// @Success 200 {object} map[string]interface{} "Инструкция отправлена"
		// @Failure 400 {object} map[string]interface{} "Некорректный email"
		// @Router /auth/forgot [post]
		authGroup.POST("/forgot", authHandler.ForgotPassword)

		// @Summary Показать форму сброса пароля
		// @Description Возвращает HTML-страницу для ввода нового пароля по токену
		// @Tags auth
		// @Accept html
		// @Produce html
		// @Param token query string true "Токен сброса пароля"
		// @Success 200 {string} html "Страница сброса пароля"
		// @Router /auth/reset [get]
		authGroup.GET("/reset", authHandler.ShowResetForm)

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
		authGroup.POST("/reset", authHandler.ResetPassword)

		// @Summary Подтверждение email
		// @Description Активирует email пользователя по токену
		// @Tags auth
		// @Produce html
		// @Param token query string true "Токен подтверждения"
		// @Success 200 {object} map[string]interface{} "Email подтверждён"
		// @Failure 400 {object} map[string]interface{} "Неверный или истёкший токен"
		// @Router /auth/verify [get]
		authGroup.GET("/verify", authHandler.VerifyEmail)

		// @Summary Начало OAuth-авторизации
		// @Description Перенаправляет на страницу авторизации провайдера (Google, GitHub, Yandex)
		// @Tags auth
		// @Param provider path string true "Провайдер OAuth (google, github, yandex)"
		// @Success 302 {string} string "Перенаправление на провайдера"
		// @Failure 400 {object} map[string]interface{} "Неподдерживаемый провайдер"
		// @Router /auth/oauth/{provider} [get]
		authGroup.GET("/oauth/:provider", oauthRateLimit, authHandler.OAuthLogin)

		// @Summary Обработка callback OAuth
		// @Description Завершает OAuth-авторизацию, создаёт/входит пользователя
		// @Tags auth
		// @Param provider path string true "Провайдер OAuth"
		// @Param code query string true "Код авторизации"
		// @Param state query string true "Состояние (state)"
		// @Success 302 {string} string "Перенаправление на /dashboard"
		// @Failure 400 {object} map[string]interface{} "Ошибка авторизации"
		// @Router /auth/oauth/{provider}/callback [get]
		authGroup.GET("/oauth/:provider/callback", oauthRateLimit, authHandler.OAuthCallback)
	}

	profileGroup := r.Group("/profile")
	profileGroup.Use(middleware.AuthRequired(authSvc))
	{
		// @Summary Личный профиль
		// @Description Отображает страницу профиля текущего пользователя
		// @Tags profile
		// @Produce html
		// @Success 200 {string} html "Страница профиля"
		// @Failure 404 {object} map[string]interface{} "Пользователь не найден"
		// @Router /profile [get]
		// @Security JWT
		profileGroup.GET("/", profileHandler.Show)

		// @Summary Загрузка аватара
		// @Description Загружает аватар пользователя
		// @Tags profile
		// @Accept multipart/form-data
		// @Produce html
		// @Param avatar formData file true "Файл аватара"
		// @Success 302 {string} string "Перенаправление на /profile"
		// @Failure 400 {object} map[string]interface{} "Ошибка загрузки"
		// @Router /profile/avatar [post]
		// @Security JWT
		profileGroup.POST("/avatar", profileHandler.UploadAvatar)

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
		profileGroup.POST("/update", profileHandler.UpdateProfile)

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
		profileGroup.POST("/change-password", profileHandler.ChangePassword)
	}

	achievementGroup := r.Group("/achievements")
	achievementGroup.Use(middleware.AuthRequired(authSvc))
	{
		// @Summary Список достижений
		// @Description Отображает все достижения текущего пользователя
		// @Tags achievements
		// @Produce html
		// @Success 200 {string} html "Страница достижений"
		// @Router /achievements [get]
		// @Security JWT
		achievementGroup.GET("/", achievementHandler.List)
	}

	dashboardGroup := r.Group("/dashboard")
	dashboardGroup.Use(middleware.AuthRequired(authSvc))
	{
		// @Summary Личный кабинет
		// @Description Отображает главную страницу личного кабинета
		// @Tags dashboard
		// @Produce html
		// @Success 200 {string} html "Страница личного кабинета"
		// @Router /dashboard [get]
		// @Security JWT
		dashboardGroup.GET("/", dashboardHandler.Index)
	}
}
