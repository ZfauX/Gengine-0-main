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
	db *gorm.DB, // добавлен для AdminRequired
) {
	auth := r.Group("/auth")
	{
		auth.GET("/login", func(c *gin.Context) {
			c.HTML(http.StatusOK, "login.html", gin.H{
				"title": "Вход",
				"csrf":  c.GetString("csrf"),
			})
		})
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

		auth.GET("/register", func(c *gin.Context) {
			c.HTML(http.StatusOK, "register.html", gin.H{
				"title": "Регистрация",
				"csrf":  c.GetString("csrf"),
			})
		})
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

		auth.GET("/logout", func(c *gin.Context) {
			c.SetCookie("jwt", "", -1, "/", "", false, true)
			c.Redirect(http.StatusFound, "/auth/login")
		})

		auth.GET("/oauth/:provider", func(c *gin.Context) {
			provider := c.Param("provider")
			url, err := oauthService.GetAuthURL(provider)
			if err != nil {
				c.String(http.StatusBadRequest, err.Error())
				return
			}
			c.Redirect(http.StatusFound, url)
		})
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

		auth.GET("/reset", func(c *gin.Context) {
			c.HTML(http.StatusOK, "reset_password.html", gin.H{
				"title": "Сброс пароля",
				"csrf":  c.GetString("csrf"),
			})
		})
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
		auth.GET("/reset/confirm", func(c *gin.Context) {
			token := c.Query("token")
			c.HTML(http.StatusOK, "reset_confirm.html", gin.H{
				"title": "Новый пароль",
				"token": token,
				"csrf":  c.GetString("csrf"),
			})
		})
		auth.POST("/reset/confirm", func(c *gin.Context) {
			token := c.PostForm("token")
			newPassword := c.PostForm("password")
			if err := passwordResetService.ResetPassword(c.Request.Context(), token, newPassword); err != nil {
				c.HTML(http.StatusBadRequest, "reset_confirm.html", gin.H{"error": err.Error()})
				return
			}
			c.Redirect(http.StatusFound, "/auth/login")
		})

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

	adminGroup := r.Group("/admin/users")
	adminGroup.Use(middleware.AuthRequired(authService), middleware.AdminRequired(db))
	{
		adminGroup.GET("/", func(c *gin.Context) {
			c.String(http.StatusOK, "Список пользователей (admin)")
		})
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
