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
	profileHandler := NewProfileHandler(db, localStorage, authSvc)
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
		// @Description Вход в систему с получением JWT-токена (устанавливается в cookie). При успешном входе также устанавливается refresh-токен.
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

		// @Summary Обновление access-токена
		// @Description Получает новый access-токен по refresh-токену (из cookie или тела запроса). Refresh-токен должен быть действительным.
		// @Tags auth
		// @Accept json
		// @Produce json
		// @Param refresh_token body string true "Refresh-токен"
		// @Success 200 {object} map[string]interface{} "Новый access-токен и время жизни"
		// @Failure 401 {object} map[string]interface{} "Невалидный refresh-токен"
		// @Router /auth/refresh [post]
		authGroup.POST("/refresh", authHandler.RefreshToken)

		// @Summary Показать форму регистрации
		// @Description Возвращает HTML-страницу с формой регистрации
		// @Tags auth
		// @Accept html
		// @Produce html
		// @Success 200 {string} html "Страница регистрации"
		// @Router /auth/register [get]
		authGroup.GET("/register", authHandler.ShowRegisterForm)

		// @Summary Регистрация пользователя
		// @Description Создаёт нового пользователя с ограничением частоты запросов (3 попытки за 10 минут с одного IP)
		// @Tags auth
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param email formData string true "Email пользователя"
		// @Param password formData string true "Пароль (минимум 8 символов, максимум 72)"
		// @Param name formData string true "Имя пользователя (2-50 символов)"
		// @Success 302 {string} string "Перенаправление на /auth/login"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Failure 409 {object} map[string]interface{} "Email уже зарегистрирован"
		// @Failure 429 {object} map[string]interface{} "Слишком много попыток регистрации"
		// @Router /auth/register [post]
		authGroup.POST("/register", middleware.RegistrationRateLimit(10*time.Minute, 3), authHandler.Register)

		// @Summary Выход из системы
		// @Description Удаляет JWT-куку и refresh-токен, перенаправляет на главную
		// @Tags auth
		// @Produce html
		// @Success 302 {string} string "Перенаправление на /"
		// @Router /auth/logout [get]
		authGroup.GET("/logout", authHandler.Logout)

		// @Summary Выход со всех устройств
		// @Description Отзывает все refresh-токены пользователя и удаляет куки
		// @Tags auth
		// @Produce html
		// @Success 302 {string} string "Перенаправление на /"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Router /auth/logout-all [post]
		// @Security JWT
		authGroup.POST("/logout-all", middleware.AuthRequired(authSvc), authHandler.LogoutAll)

		// @Summary Показать форму восстановления пароля
		// @Description Возвращает HTML-страницу для запроса сброса пароля
		// @Tags auth
		// @Accept html
		// @Produce html
		// @Success 200 {string} html "Страница восстановления"
		// @Router /auth/forgot [get]
		authGroup.GET("/forgot", authHandler.ShowForgotForm)

		// @Summary Запрос на сброс пароля
		// @Description Отправляет на email ссылку для установки нового пароля (если пользователь с таким email существует)
		// @Tags auth
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param email formData string true "Email пользователя"
		// @Success 200 {object} map[string]interface{} "Инструкция отправлена"
		// @Failure 400 {object} map[string]interface{} "Некорректный email"
		// @Router /auth/forgot [post]
		authGroup.POST("/forgot", authHandler.ForgotPassword)

		// @Summary Показать форму сброса пароля
		// @Description Возвращает HTML-страницу для ввода нового пароля по токену, полученному по email
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
		// @Description Активирует email пользователя по токену, отправленному при регистрации
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
		// @Description Завершает OAuth-авторизацию, создаёт или входит пользователя, устанавливает JWT и refresh-токен
		// @Tags auth
		// @Param provider path string true "Провайдер OAuth"
		// @Param code query string true "Код авторизации"
		// @Param state query string true "Состояние (state) для защиты от CSRF"
		// @Success 302 {string} string "Перенаправление на /dashboard"
		// @Failure 400 {object} map[string]interface{} "Ошибка авторизации"
		// @Router /auth/oauth/{provider}/callback [get]
		authGroup.GET("/oauth/:provider/callback", oauthRateLimit, authHandler.OAuthCallback)
	}

	profileGroup := r.Group("/profile")
	profileGroup.Use(middleware.AuthRequired(authSvc))
	{
		// @Summary Личный профиль
		// @Description Отображает страницу профиля текущего пользователя с возможностью редактирования
		// @Tags profile
		// @Produce html
		// @Success 200 {string} html "Страница профиля"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Failure 404 {object} map[string]interface{} "Пользователь не найден"
		// @Router /profile [get]
		// @Security JWT
		profileGroup.GET("/", profileHandler.Show)

		// @Summary Загрузка аватара
		// @Description Загружает аватар пользователя (до 2 МБ, поддерживаются JPEG, PNG, WebP)
		// @Tags profile
		// @Accept multipart/form-data
		// @Produce html
		// @Param avatar formData file true "Файл аватара"
		// @Success 302 {string} string "Перенаправление на /profile"
		// @Failure 400 {object} map[string]interface{} "Ошибка загрузки"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Router /profile/avatar [post]
		// @Security JWT
		profileGroup.POST("/avatar", profileHandler.UploadAvatar)

		// @Summary Обновление профиля
		// @Description Изменяет имя и email текущего пользователя
		// @Tags profile
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param name formData string true "Новое имя (2-50 символов)"
		// @Param email formData string true "Новый email"
		// @Success 302 {string} string "Перенаправление на /profile"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Router /profile/update [post]
		// @Security JWT
		profileGroup.POST("/update", profileHandler.UpdateProfile)

		// @Summary Смена пароля
		// @Description Изменяет пароль текущего пользователя (требуется ввод текущего пароля)
		// @Tags profile
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param old_password formData string true "Текущий пароль"
		// @Param new_password formData string true "Новый пароль (минимум 8 символов)"
		// @Success 302 {string} string "Перенаправление на /profile"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации или неверный пароль"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Router /profile/change-password [post]
		// @Security JWT
		profileGroup.POST("/change-password", profileHandler.ChangePassword)
	}

	achievementGroup := r.Group("/achievements")
	achievementGroup.Use(middleware.AuthRequired(authSvc))
	{
		// @Summary Список достижений
		// @Description Отображает все достижения текущего пользователя (полученные награды)
		// @Tags achievements
		// @Produce html
		// @Success 200 {string} html "Страница достижений"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Router /achievements [get]
		// @Security JWT
		achievementGroup.GET("/", achievementHandler.List)
	}

	dashboardGroup := r.Group("/dashboard")
	dashboardGroup.Use(middleware.AuthRequired(authSvc))
	{
		// @Summary Личный кабинет
		// @Description Отображает главную страницу личного кабинета с информацией о созданных играх, командах, прохождениях и приглашениях
		// @Tags dashboard
		// @Produce html
		// @Success 200 {string} html "Страница личного кабинета"
		// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
		// @Router /dashboard [get]
		// @Security JWT
		dashboardGroup.GET("/", dashboardHandler.Index)
	}

	// ============================================================
	// ПУБЛИЧНЫЙ ПРОФИЛЬ ПОЛЬЗОВАТЕЛЯ
	// ============================================================
	usersGroup := r.Group("/users")
	usersGroup.Use(middleware.OptionalAuth(authSvc))
	{
		// @Summary Публичный профиль пользователя
		// @Description Отображает публичный профиль пользователя по ID
		// @Tags profile
		// @Produce html
		// @Param id path int true "ID пользователя"
		// @Success 200 {string} html "Страница публичного профиля"
		// @Failure 404 {object} map[string]interface{} "Пользователь не найден"
		// @Router /users/{id} [get]
		usersGroup.GET("/:id", profileHandler.PublicProfile)
	}
}
