// internal/domain/user/routes.go
package user

import (
	"net/http"
	"strconv"

	"gengine-0/internal/config"
	"gengine-0/internal/pkg/audit"
	"gengine-0/internal/pkg/middleware"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterRoutes регистрирует маршруты для пользователей.
// @tags auth
// @tags profile
// @tags achievements
// @tags dashboard
func RegisterRoutes(
	r *gin.Engine,
	authService *AuthService,
	userService *UserService,
	achievService *AchievementService,
	oauthService *OAuthService,
	passwordResetService *PasswordResetService,
	emailVerifService *EmailVerificationService,
	cfg *config.Config,
	auditSvc *audit.Service,
	db *gorm.DB,
) {
	auth := r.Group("/auth")
	{
		// @Summary Показать форму входа
		// @Description Возвращает HTML-страницу с формой входа
		// @Tags auth
		// @Accept html
		// @Produce html
		// @Success 200 {string} html "Страница входа"
		// @Router /auth/login [get]
		auth.GET("/login", func(c *gin.Context) {
			c.HTML(http.StatusOK, "login.html", gin.H{
				"title": "Вход",
				"csrf":  c.GetString("csrf"),
			})
		})

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
		auth.POST("/login", func(c *gin.Context) {
			var input LoginInput
			if err := c.ShouldBind(&input); err != nil {
				c.HTML(http.StatusBadRequest, "login.html", gin.H{"error": "Неверные данные"})
				return
			}
			token, err := authService.Login(c.Request.Context(), input.Email, input.Password)
			if err != nil {
				c.HTML(http.StatusUnauthorized, "login.html", gin.H{"error": err.Error()})
				return
			}
			c.SetCookie("jwt", token, int(cfg.JWT.AccessExpiry.Seconds()), "/", "", false, true)
			c.Redirect(http.StatusFound, "/")
		})

		// @Summary Показать форму регистрации
		// @Description Возвращает HTML-страницу с формой регистрации
		// @Tags auth
		// @Accept html
		// @Produce html
		// @Success 200 {string} html "Страница регистрации"
		// @Router /auth/register [get]
		auth.GET("/register", func(c *gin.Context) {
			c.HTML(http.StatusOK, "register.html", gin.H{
				"title": "Регистрация",
				"csrf":  c.GetString("csrf"),
			})
		})

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
		auth.POST("/register", func(c *gin.Context) {
			var input RegisterInput
			if err := c.ShouldBind(&input); err != nil {
				c.HTML(http.StatusBadRequest, "register.html", gin.H{"error": err.Error()})
				return
			}
			user, err := authService.Register(c.Request.Context(), input.Email, input.Password, input.Name)
			if err != nil {
				c.HTML(http.StatusBadRequest, "register.html", gin.H{"error": err.Error()})
				return
			}
			token, _ := authService.GenerateJWT(*user)
			c.SetCookie("jwt", token, int(cfg.JWT.AccessExpiry.Seconds()), "/", "", false, true)
			c.Redirect(http.StatusFound, "/")
		})

		// @Summary Выход из системы
		// @Description Удаляет JWT-куку и перенаправляет на главную
		// @Tags auth
		// @Produce html
		// @Success 302 {string} string "Перенаправление на /"
		// @Router /auth/logout [get]
		auth.GET("/logout", func(c *gin.Context) {
			c.SetCookie("jwt", "", -1, "/", "", false, true)
			c.Redirect(http.StatusFound, "/auth/login")
		})

		// @Summary Начало OAuth-авторизации
		// @Description Перенаправляет на страницу авторизации провайдера (Google, GitHub, Yandex)
		// @Tags auth
		// @Param provider path string true "Провайдер OAuth (google, github, yandex)"
		// @Success 302 {string} string "Перенаправление на провайдера"
		// @Failure 400 {object} map[string]interface{} "Неподдерживаемый провайдер"
		// @Router /auth/oauth/{provider} [get]
		auth.GET("/oauth/:provider", func(c *gin.Context) {
			provider := c.Param("provider")
			url, err := oauthService.GetAuthURL(provider)
			if err != nil {
				c.String(http.StatusBadRequest, err.Error())
				return
			}
			c.Redirect(http.StatusFound, url)
		})

		// @Summary Обработка callback OAuth
		// @Description Завершает OAuth-авторизацию, создаёт/входит пользователя
		// @Tags auth
		// @Param provider path string true "Провайдер OAuth"
		// @Param code query string true "Код авторизации"
		// @Param state query string true "Состояние (state)"
		// @Success 302 {string} string "Перенаправление на /dashboard"
		// @Failure 400 {object} map[string]interface{} "Ошибка авторизации"
		// @Router /auth/oauth/{provider}/callback [get]
		auth.GET("/oauth/:provider/callback", func(c *gin.Context) {
			provider := c.Param("provider")
			code := c.Query("code")
			state := c.Query("state")
			user, err := oauthService.Authenticate(c.Request.Context(), provider, code, state)
			if err != nil {
				c.String(http.StatusBadRequest, err.Error())
				return
			}
			token, _ := authService.GenerateJWT(*user)
			c.SetCookie("jwt", token, int(cfg.JWT.AccessExpiry.Seconds()), "/", "", false, true)
			c.Redirect(http.StatusFound, "/")
		})

		// @Summary Показать форму восстановления пароля
		// @Description Возвращает HTML-страницу для запроса сброса пароля
		// @Tags auth
		// @Accept html
		// @Produce html
		// @Success 200 {string} html "Страница восстановления"
		// @Router /auth/reset [get]
		auth.GET("/reset", func(c *gin.Context) {
			c.HTML(http.StatusOK, "reset_password.html", gin.H{
				"title": "Сброс пароля",
				"csrf":  c.GetString("csrf"),
			})
		})

		// @Summary Запрос на сброс пароля
		// @Description Отправляет на email ссылку для установки нового пароля
		// @Tags auth
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param email formData string true "Email пользователя"
		// @Success 200 {object} map[string]interface{} "Инструкция отправлена"
		// @Failure 400 {object} map[string]interface{} "Некорректный email"
		// @Router /auth/reset [post]
		auth.POST("/reset", func(c *gin.Context) {
			email := c.PostForm("email")
			user, err := userService.GetByEmail(c.Request.Context(), email)
			if err != nil {
				c.HTML(http.StatusBadRequest, "reset_password.html", gin.H{"error": "Пользователь не найден"})
				return
			}
			_, err = passwordResetService.GenerateToken(c.Request.Context(), *user)
			if err != nil {
				c.HTML(http.StatusInternalServerError, "reset_password.html", gin.H{"error": "Ошибка отправки письма"})
				return
			}
			c.HTML(http.StatusOK, "reset_password.html", gin.H{"success": "Инструкция отправлена на email"})
		})

		// @Summary Показать форму сброса пароля
		// @Description Возвращает HTML-страницу для ввода нового пароля по токену
		// @Tags auth
		// @Accept html
		// @Produce html
		// @Param token query string true "Токен сброса пароля"
		// @Success 200 {string} html "Страница сброса пароля"
		// @Router /auth/reset/confirm [get]
		auth.GET("/reset/confirm", func(c *gin.Context) {
			token := c.Query("token")
			c.HTML(http.StatusOK, "reset_confirm.html", gin.H{
				"title": "Новый пароль",
				"token": token,
				"csrf":  c.GetString("csrf"),
			})
		})

		// @Summary Сброс пароля
		// @Description Устанавливает новый пароль по токену, полученному по email
		// @Tags auth
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param token formData string true "Токен сброса пароля"
		// @Param password formData string true "Новый пароль (минимум 8 символов)"
		// @Success 302 {string} string "Перенаправление на /auth/login"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации или неверный токен"
		// @Router /auth/reset/confirm [post]
		auth.POST("/reset/confirm", func(c *gin.Context) {
			token := c.PostForm("token")
			newPassword := c.PostForm("password")
			if err := passwordResetService.ResetPassword(c.Request.Context(), token, newPassword); err != nil {
				c.HTML(http.StatusBadRequest, "reset_confirm.html", gin.H{"error": err.Error()})
				return
			}
			c.Redirect(http.StatusFound, "/auth/login")
		})

		// @Summary Подтверждение email
		// @Description Активирует email пользователя по токену
		// @Tags auth
		// @Accept json
		// @Produce html
		// @Param token query string true "Токен подтверждения"
		// @Success 200 {object} map[string]interface{} "Email подтверждён"
		// @Failure 400 {object} map[string]interface{} "Неверный или истёкший токен"
		// @Router /auth/verify [get]
		auth.GET("/verify", func(c *gin.Context) {
			token := c.Query("token")
			user, err := emailVerifService.VerifyToken(c.Request.Context(), token)
			if err != nil {
				c.String(http.StatusBadRequest, err.Error())
				return
			}
			c.HTML(http.StatusOK, "verify_success.html", gin.H{
				"title": "Email подтверждён",
				"user":  user,
			})
		})
	}

	protected := r.Group("/profile")
	protected.Use(middleware.AuthRequired(authService))
	{
		// @Summary Личный профиль
		// @Description Отображает страницу профиля текущего пользователя
		// @Tags profile
		// @Produce html
		// @Success 200 {string} html "Страница профиля"
		// @Failure 404 {object} map[string]interface{} "Пользователь не найден"
		// @Router /profile [get]
		// @Security JWT
		protected.GET("/", func(c *gin.Context) {
			userID := c.GetUint("user_id")
			user, err := userService.GetPublicProfile(c.Request.Context(), userID)
			if err != nil {
				c.String(http.StatusNotFound, "Пользователь не найден")
				return
			}
			c.HTML(http.StatusOK, "profile.html", gin.H{
				"title": "Профиль",
				"user":  user,
			})
		})

		// @Summary Форма редактирования профиля
		// @Description Возвращает HTML-страницу с формой для редактирования профиля
		// @Tags profile
		// @Produce html
		// @Success 200 {string} html "Форма редактирования профиля"
		// @Router /profile/edit [get]
		// @Security JWT
		protected.GET("/edit", func(c *gin.Context) {
			userID := c.GetUint("user_id")
			user, err := userService.GetByID(c.Request.Context(), userID)
			if err != nil {
				c.String(http.StatusNotFound, "Пользователь не найден")
				return
			}
			c.HTML(http.StatusOK, "profile_edit.html", gin.H{
				"title": "Редактировать профиль",
				"user":  user,
				"csrf":  c.GetString("csrf"),
			})
		})

		// @Summary Обновление профиля
		// @Description Изменяет имя и email текущего пользователя
		// @Tags profile
		// @Accept x-www-form-urlencoded
		// @Produce html
		// @Param name formData string true "Новое имя"
		// @Param email formData string true "Новый email"
		// @Success 302 {string} string "Перенаправление на /profile"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Router /profile/edit [post]
		// @Security JWT
		protected.POST("/edit", func(c *gin.Context) {
			userID := c.GetUint("user_id")
			name := c.PostForm("name")
			email := c.PostForm("email")
			if err := userService.UpdateProfile(c.Request.Context(), userID, name, email); err != nil {
				c.HTML(http.StatusBadRequest, "profile_edit.html", gin.H{"error": err.Error()})
				return
			}
			c.Redirect(http.StatusFound, "/profile")
		})

		// @Summary Список достижений
		// @Description Отображает все достижения текущего пользователя
		// @Tags achievements
		// @Produce html
		// @Success 200 {string} html "Страница достижений"
		// @Router /profile/achievements [get]
		// @Security JWT
		protected.GET("/achievements", func(c *gin.Context) {
			userID := c.GetUint("user_id")
			achievements, err := achievService.GetUserAchievements(c.Request.Context(), userID)
			if err != nil {
				c.String(http.StatusInternalServerError, err.Error())
				return
			}
			c.HTML(http.StatusOK, "achievements.html", gin.H{
				"title":        "Достижения",
				"achievements": achievements,
			})
		})

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
		protected.POST("/change-password", func(c *gin.Context) {
			userID := c.GetUint("user_id")
			oldPassword := c.PostForm("old_password")
			newPassword := c.PostForm("new_password")
			if err := userService.ChangePassword(c.Request.Context(), userID, oldPassword, newPassword); err != nil {
				c.HTML(http.StatusBadRequest, "profile_edit.html", gin.H{"error": err.Error()})
				return
			}
			c.Redirect(http.StatusFound, "/profile")
		})
	}

	// +++ Исправлено: убрана передача db в AdminRequired
	adminGroup := r.Group("/admin/users")
	adminGroup.Use(middleware.AuthRequired(authService), middleware.AdminRequired())
	{
		// @Summary Список пользователей (административный)
		// @Description Возвращает список всех пользователей (доступно только администратору)
		// @Tags admin
		// @Produce html
		// @Success 200 {string} html "Страница пользователей"
		// @Router /admin/users [get]
		// @Security JWT
		adminGroup.GET("/", func(c *gin.Context) {
			c.String(http.StatusOK, "Список пользователей (admin)")
		})

		// @Summary Получить пользователя (административный)
		// @Description Возвращает JSON-данные пользователя по ID (доступно только администратору)
		// @Tags admin
		// @Produce json
		// @Param id path int true "ID пользователя"
		// @Success 200 {object} map[string]interface{} "Данные пользователя"
		// @Failure 404 {object} map[string]interface{} "Пользователь не найден"
		// @Router /admin/users/{id} [get]
		// @Security JWT
		adminGroup.GET("/:id", func(c *gin.Context) {
			id, _ := strconv.Atoi(c.Param("id"))
			user, err := userService.GetByID(c.Request.Context(), uint(id))
			if err != nil {
				c.String(http.StatusNotFound, "Пользователь не найден")
				return
			}
			c.JSON(http.StatusOK, user)
		})
	}
}
