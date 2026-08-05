// internal/domain/user/auth_handler.go
package user

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/pkg/audit"
	"gengine-0/internal/pkg/email"
	apperrors "gengine-0/internal/pkg/errors"
	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/sanitize"
	"gengine-0/internal/pkg/validation"

	csrf "gengine-0/internal/pkg/csrf"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

type AuthHandler struct {
	cfg                  *config.Config
	authSvc              *AuthService
	userService          *UserService
	passwordResetSvc     *PasswordResetService
	emailVerificationSvc *EmailVerificationService
	oauthSvc             *OAuthService
	auditSvc             *audit.Service
	emailSvc             *email.EmailService
	twoFactorSvc         *TwoFactorService
}

func NewAuthHandler(
	cfg *config.Config,
	authSvc *AuthService,
	userService *UserService,
	passwordResetSvc *PasswordResetService,
	emailVerificationSvc *EmailVerificationService,
	oauthSvc *OAuthService,
	auditSvc *audit.Service,
	emailSvc *email.EmailService,
	twoFactorSvc *TwoFactorService,
) *AuthHandler {
	return &AuthHandler{
		cfg:                  cfg,
		authSvc:              authSvc,
		userService:          userService,
		passwordResetSvc:     passwordResetSvc,
		emailVerificationSvc: emailVerificationSvc,
		oauthSvc:             oauthSvc,
		auditSvc:             auditSvc,
		emailSvc:             emailSvc,
		twoFactorSvc:         twoFactorSvc,
	}
}

// ShowLoginForm отображает форму входа.
// @Summary Показать форму входа
// @Description Возвращает HTML-страницу с формой входа в систему
// @Tags auth
// @Accept html
// @Produce html
// @Success 200 {string} html "Страница входа"
// @Router /auth/login [get]
func (h *AuthHandler) ShowLoginForm(c *gin.Context) {
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
	render.Page(c, http.StatusOK, "auth-login.html", gin.H{
		"Title": render.Tr(c, "auth.login_title"),
		"csrf":  csrf.GetToken(c),
		"Breadcrumbs": []map[string]string{
			{"name": "nav.home", "url": "/"},
			{"name": "nav.login"},
		},
	})
}

// Login аутентифицирует пользователя.
// @Summary Аутентификация пользователя
// @Description Вход в систему по email и паролю. При успехе устанавливает JWT-куку
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
	errs := validation.FieldErrors{}
	if err := c.ShouldBind(&input); err != nil {
		errs.Add("email", validation.ValidateString("Email", input.Email, 1, 255))
		errs.Add("password", validation.ValidateString("Пароль", input.Password, 1, 128))
		if !errs.HasErrors() {
			errs.Add("form", fmt.Errorf("некорректные данные: %v", err))
		}
		render.Page(c, http.StatusBadRequest, "auth-login.html", gin.H{
			"Title":  "Вход",
			"Errors": errs,
			"Error":  errs.Error(),
			"csrf":   csrf.GetToken(c),
		})
		return
	}

	token, err := h.authSvc.Login(c.Request.Context(), input.Email, input.Password)
	if err != nil {
		render.Page(c, http.StatusUnauthorized, "auth-login.html", gin.H{
			"Title":  "Вход",
			"Errors": validation.FieldErrors{"email": "Неверный email или пароль"},
			"Error":  "Неверный email или пароль",
			"csrf":   csrf.GetToken(c),
		})
		return
	}

	// Check if user has 2FA enabled — require TOTP before issuing tokens
	user, err := h.userService.GetByEmail(c.Request.Context(), input.Email)
	if err != nil {
		// Fail closed: don't issue tokens if we can't confirm 2FA status
		log.Error().Err(err).Str("email", input.Email).Msg("Login: failed to fetch user for 2FA check")
		render.Page(c, http.StatusUnauthorized, "auth-login.html", gin.H{
			"Title":  "Вход",
			"Errors": validation.FieldErrors{"email": "Неверный email или пароль"},
			"Error":  "Неверный email или пароль",
			"csrf":   csrf.GetToken(c),
		})
		return
	}
	if user != nil && user.TwoFactorEnabled {
		// Store pending login info in session (no JWT stored — only issued after TOTP)
		sess := sessions.Default(c)
		sess.Set("pending_user_id", user.ID)
		sess.Set("pending_email", user.Email)
		if err := sess.Save(); err != nil {
			log.Error().Err(err).Msg("Login: failed to save 2FA pending session")
		}
		c.Redirect(http.StatusFound, "/auth/2fa/login?return_url=/dashboard")
		return
	}

	setSecureCookie(c, "jwt", token, int(h.cfg.JWT.AccessExpiry.Seconds()), "/")

	if user != nil {
		deviceID := c.GetHeader("X-Device-ID")
		refreshToken, err := h.authSvc.GenerateRefreshToken(c.Request.Context(), *user, deviceID, clientFingerprint(c))
		if err == nil {
			setSecureCookie(c, "refresh_token", refreshToken, int(h.cfg.JWT.RefreshExpiry.Seconds()), "/auth/refresh")
		} else {
			log.Error().Err(err).Msg("Login: failed to generate refresh token")
		}
	}

	userID, _, parseErr := h.authSvc.ParseToken(token)
	if parseErr != nil {
		log.Error().Err(parseErr).Msg("Login: failed to parse token for audit")
	} else {
		email := input.Email
		if user != nil {
			email = user.Email
		}
		h.auditSvc.Log(userID, "login", "user", userID, email)
	}

	c.Redirect(http.StatusFound, "/dashboard")
}

// TwoFALoginForm отображает форму ввода TOTP-кода после логина.
func (h *AuthHandler) TwoFALoginForm(c *gin.Context) {
	sess := sessions.Default(c)
	if sess.Get("pending_user_id") == nil {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}
	returnURL := safeReturnURL(c.DefaultQuery("return_url", ""), "/dashboard")
	render.Page(c, http.StatusOK, "auth-login-2fa.html", gin.H{
		"Title":     "Двухфакторная аутентификация",
		"ReturnURL": returnURL,
		"csrf":      csrf.GetToken(c),
	})
}

// TwoFALoginVerify проверяет TOTP-код и завершает логин.
func (h *AuthHandler) TwoFALoginVerify(c *gin.Context) {
	sess := sessions.Default(c)
	pendingUserID, ok := sess.Get("pending_user_id").(uint)
	if !ok || pendingUserID == 0 {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}

	code := c.PostForm("code")
	returnURL := safeReturnURL(c.DefaultPostForm("return_url", ""), "/dashboard")

	if err := h.twoFactorSvc.Validate2FAInput(code); err != nil {
		render.Page(c, http.StatusOK, "auth-login-2fa.html", gin.H{
			"Title":     "Двухфакторная аутентификация",
			"Error":     err.Error(),
			"ReturnURL": returnURL,
			"csrf":      csrf.GetToken(c),
		})
		return
	}

	// Fetch user to get TOTP secret
	user, err := h.userService.GetByID(c.Request.Context(), pendingUserID)
	if err != nil {
		render.RenderErrorPage(c, http.StatusNotFound)
		return
	}

	valid, err := h.twoFactorSvc.VerifyCode(user.TwoFactorSecret, code)
	if err != nil || !valid {
		render.Page(c, http.StatusOK, "auth-login-2fa.html", gin.H{
			"Title":     "Двухфакторная аутентификация",
			"Error":     render.Tr(c, "handler.wrong_code_try_again"),
			"ReturnURL": returnURL,
			"csrf":      csrf.GetToken(c),
		})
		return
	}

	// TOTP verified — generate JWT and issue tokens
	token, jwtErr := h.authSvc.GenerateJWT(*user)
	if jwtErr != nil {
		log.Error().Err(jwtErr).Uint("user_id", user.ID).Msg("TwoFALoginVerify: failed to generate JWT")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	setSecureCookie(c, "jwt", token, int(h.cfg.JWT.AccessExpiry.Seconds()), "/")
	deviceID := c.GetHeader("X-Device-ID")
	refreshToken, err := h.authSvc.GenerateRefreshToken(c.Request.Context(), *user, deviceID, clientFingerprint(c))
	if err == nil {
		setSecureCookie(c, "refresh_token", refreshToken, int(h.cfg.JWT.RefreshExpiry.Seconds()), "/auth/refresh")
	} else {
		log.Error().Err(err).Uint("user_id", user.ID).Msg("TwoFALoginVerify: failed to generate refresh token")
	}

	// Clear pending login from session
	sess.Delete("pending_user_id")
	sess.Delete("pending_email")
	if err := sess.Save(); err != nil {
		log.Error().Err(err).Msg("TwoFALoginVerify: failed to clear pending session")
	}

	h.auditSvc.Log(user.ID, "login", "user", user.ID, user.Email)
	c.Redirect(http.StatusFound, returnURL)
}

// TODO: JWT blacklist via jti would need Valkey support — check jti against a denylist
// before accepting a token in middleware.

// RefreshToken обновляет access-токен.
// @Summary Обновление access-токена
// @Description Получает новый access-токен по refresh-токену
// @Tags auth
// @Accept json
// @Produce json
// @Param refresh_token body string true "Refresh-токен"
// @Success 200 {object} map[string]interface{} "Новый access-токен и время жизни"
// @Failure 401 {object} map[string]interface{} "Невалидный refresh-токен"
// @Router /auth/refresh [post]
func (h *AuthHandler) RefreshToken(c *gin.Context) {
	refreshToken, err := c.Cookie("refresh_token")
	if err != nil || refreshToken == "" {
		var input RefreshTokenInput
		if bindErr := c.ShouldBindJSON(&input); bindErr != nil || input.RefreshToken == "" {
			appErr := apperrors.Unauthorized("refresh token required")
			c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
				"error": appErr.Message,
				"code":  appErr.Code,
			})
			return
		}
		refreshToken = input.RefreshToken
	}

	deviceID := c.GetHeader("X-Device-ID")
	accessToken, newRefreshToken, err := h.authSvc.RefreshAccessToken(c.Request.Context(), refreshToken, deviceID, clientFingerprint(c))
	if err != nil {
		appErr := apperrors.Unauthorized(render.LocalizeError(c, err.Error()))
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	setSecureCookie(c, "jwt", accessToken, int(h.cfg.JWT.AccessExpiry.Seconds()), "/")
	setSecureCookie(c, "refresh_token", newRefreshToken, int(h.cfg.JWT.RefreshExpiry.Seconds()), "/auth/refresh")

	c.JSON(http.StatusOK, gin.H{
		"access_token": accessToken,
		"expires_in":   int(h.cfg.JWT.AccessExpiry.Seconds()),
	})
}

// Logout выполняет выход из системы.
// @Summary Выход из системы
// @Description Удаляет JWT-куку и завершает сессию
// @Tags auth
// @Produce html
// @Success 302 {string} string "Перенаправление на /"
// @Router /auth/logout [post]
func (h *AuthHandler) Logout(c *gin.Context) {
	// Revoke JWT via JTI blacklist
	if jwtCookie, err := c.Cookie("jwt"); err == nil && jwtCookie != "" {
		h.authSvc.RevokeJWT(c.Request.Context(), jwtCookie)
	}
	refreshTokenCookie, err := c.Cookie("refresh_token")
	if err == nil && refreshTokenCookie != "" {
		if err := h.authSvc.RevokeRefreshToken(c.Request.Context(), refreshTokenCookie); err != nil {
			log.Warn().Err(err).Msg("Logout: failed to revoke refresh token")
		}
	}
	setSecureCookie(c, "jwt", "", -1, "/")
	setSecureCookie(c, "refresh_token", "", -1, "/auth/refresh")
	clear2FASessionFlag(c)
	c.Redirect(http.StatusFound, "/")
}

// LogoutAll выполняет выход со всех устройств.
// @Summary Выход со всех устройств
// @Description Отзывает все refresh-токены пользователя
// @Tags auth
// @Produce html
// @Success 302 {string} string "Перенаправление на /"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /auth/logout-all [post]
// @Security JWT
func (h *AuthHandler) LogoutAll(c *gin.Context) {
	userID := c.GetUint("userID")
	if userID == 0 {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}
	if err := h.authSvc.RevokeAllUserTokens(c.Request.Context(), userID); err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("LogoutAll: failed to revoke tokens")
	}
	setSecureCookie(c, "jwt", "", -1, "/")
	setSecureCookie(c, "refresh_token", "", -1, "/auth/refresh")
	clear2FASessionFlag(c)
	c.Redirect(http.StatusFound, "/")
}

// ShowRegisterForm отображает форму регистрации.
// @Summary Показать форму регистрации
// @Description Возвращает HTML-страницу с формой регистрации нового пользователя
// @Tags auth
// @Accept html
// @Produce html
// @Success 200 {string} html "Страница регистрации"
// @Router /auth/register [get]
func (h *AuthHandler) ShowRegisterForm(c *gin.Context) {
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
	render.Page(c, http.StatusOK, "auth-register.html", gin.H{
		"Title": render.Tr(c, "auth.register_title"),
		"csrf":  csrf.GetToken(c),
		"Breadcrumbs": []map[string]string{
			{"name": "nav.home", "url": "/"},
			{"name": "nav.register"},
		},
	})
}

// Register регистрирует нового пользователя.
// @Summary Регистрация пользователя
// @Description Создаёт нового пользователя с указанными email, паролем и именем. Требуется подтверждение email
// @Tags auth
// @Accept x-www-form-urlencoded
// @Produce html
// @Param email formData string true "Email"
// @Param password formData string true "Пароль (минимум 8 символов)"
// @Param name formData string true "Имя пользователя"
// @Success 302 {string} string "Перенаправление на /auth/login"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Failure 409 {object} map[string]interface{} "Email уже занят"
// @Failure 429 {object} map[string]interface{} "Слишком много запросов"
// @Router /auth/register [post]
func (h *AuthHandler) Register(c *gin.Context) {
	var input RegisterInput
	errs := validation.FieldErrors{}
	if err := c.ShouldBind(&input); err != nil {
		errs.Add("name", validation.ValidateString("Имя", input.Name, 1, 128))
		errs.Add("email", validation.ValidateString("Email", input.Email, 1, 255))
		errs.Add("password", validation.ValidateString("Пароль", input.Password, 6, 128))
		if !errs.HasErrors() {
			errs.Add("form", fmt.Errorf("некорректные данные: %v", err))
		}
		render.Page(c, http.StatusBadRequest, "auth-register.html", gin.H{
			"Title":  "Регистрация",
			"Errors": errs,
			"Error":  errs.Error(),
			"csrf":   csrf.GetToken(c),
		})
		return
	}

	cleanName := sanitize.StripHTML(input.Name)
	cleanEmail := sanitize.StripHTML(input.Email)

	if err := validation.ValidatePasswordStrength(input.Password); err != nil {
		render.Page(c, http.StatusBadRequest, "auth-register.html", gin.H{
			"Title":  "Регистрация",
			"Errors": validation.FieldErrors{"password": err.Error()},
			"Error":  render.LocalizeError(c, err.Error()),
			"csrf":   csrf.GetToken(c),
		})
		return
	}

	user, err := h.authSvc.Register(c.Request.Context(), cleanEmail, input.Password, cleanName)
	if err != nil {
		render.Page(c, http.StatusOK, "auth-register.html", gin.H{
			"Title":   "Регистрация",
			"Success": "Если регистрация прошла успешно, проверьте вашу почту",
			"csrf":    csrf.GetToken(c),
		})
		return
	}

	h.auditSvc.Log(user.ID, "register", "user", user.ID, cleanEmail)
	render.SetFlash(c, "success", "Регистрация успешна! Проверьте email для подтверждения.")
	c.Redirect(http.StatusFound, "/auth/login")
}

// ShowForgotForm отображает форму восстановления пароля.
// @Summary Показать форму восстановления пароля
// @Description Возвращает HTML-страницу с формой запроса на сброс пароля
// @Tags auth
// @Accept html
// @Produce html
// @Success 200 {string} html "Форма восстановления"
// @Router /auth/forgot [get]
func (h *AuthHandler) ShowForgotForm(c *gin.Context) {
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
	render.Page(c, http.StatusOK, "auth-forgot.html", gin.H{
		"Title": render.Tr(c, "auth.forgot_title"),
		"csrf":  csrf.GetToken(c),
	})
}

// ForgotPassword отправляет ссылку на сброс пароля.
// @Summary Запрос на сброс пароля
// @Description Отправляет на email ссылку для сброса пароля
// @Tags auth
// @Accept x-www-form-urlencoded
// @Produce html
// @Param email formData string true "Email"
// @Success 200 {string} html "Страница с сообщением об отправке"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Router /auth/forgot [post]
func (h *AuthHandler) ForgotPassword(c *gin.Context) {
	var input ForgotInput
	errs := validation.FieldErrors{}
	if err := c.ShouldBind(&input); err != nil {
		errs.Add("email", validation.ValidateString("Email", input.Email, 1, 255))
		if !errs.HasErrors() {
			errs.Add("email", fmt.Errorf("некорректный email"))
		}
		render.Page(c, http.StatusBadRequest, "auth-forgot.html", gin.H{
			"Title":  "Восстановление пароля",
			"Errors": errs,
			"Error":  errs.Error(),
			"csrf":   csrf.GetToken(c),
		})
		return
	}

	user, err := h.userService.GetByEmail(c.Request.Context(), input.Email)
	if err != nil {
		log.Debug().Str("email", input.Email).Msg("ForgotPassword: user not found")
	} else {
		resetCode, genErr := h.passwordResetSvc.GenerateToken(c.Request.Context(), *user)
		if genErr != nil {
			log.Error().Err(genErr).Str("email", input.Email).Msg("ForgotPassword: failed to generate token")
		} else if !h.cfg.SMTP.Enabled {
			// Mask reset code in logs for security — show first 4 chars only
			maskedCode := resetCode
			if len(maskedCode) > 4 {
				maskedCode = maskedCode[:4] + "****"
			}
			log.Info().Str("email", input.Email).Str("reset_code", maskedCode).Msg("ForgotPassword: reset link (SMTP disabled, see log)")
		} else if h.emailSvc != nil {
			resetURL := h.cfg.Server.BaseURL + "/auth/reset/" + resetCode
			subject := "Восстановление пароля"
			body := fmt.Sprintf("Здравствуйте, %s!\n\nДля восстановления пароля перейдите по ссылке:\n%s\n\nЕсли вы не запрашивали восстановление пароля, проигнорируйте это письмо.\n\nС уважением,\nКоманда Gengine", user.Name, resetURL)
			if sendErr := h.emailSvc.Send(user.Email, subject, body); sendErr != nil {
				log.Error().Err(sendErr).Str("email", input.Email).Msg("ForgotPassword: failed to queue password reset email")
			}
		}
	}

	// Всегда показываем одинаковое сообщение — не раскрываем, существует ли email
	render.Page(c, http.StatusOK, "auth-forgot.html", gin.H{
		"Title":   "Восстановление пароля",
		"Message": "Если указанный email зарегистрирован, мы отправили инструкции по восстановлению пароля",
		"csrf":    csrf.GetToken(c),
	})
}

// ShowResetForm отображает форму сброса пароля.
// @Summary Показать форму сброса пароля
// @Description Возвращает HTML-страницу с формой установки нового пароля по коду сброса
// @Tags auth
// @Accept html
// @Produce html
// @Param resetCode path string true "Код сброса пароля"
// @Success 200 {string} html "Форма сброса пароля"
// @Router /auth/reset/{resetCode} [get]
func (h *AuthHandler) ShowResetForm(c *gin.Context) {
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
	resetCode := sanitize.StripHTML(c.Param("resetCode"))
	if resetCode == "" {
		render.RenderErrorPage(c, http.StatusBadRequest)
		return
	}
	render.Page(c, http.StatusOK, "auth-reset.html", gin.H{
		"Title":     "Сброс пароля",
		"ResetCode": resetCode,
		"csrf":      csrf.GetToken(c),
	})
}

// ResetPassword устанавливает новый пароль.
// @Summary Сброс пароля
// @Description Устанавливает новый пароль по токену сброса
// @Tags auth
// @Accept x-www-form-urlencoded
// @Produce html
// @Param token formData string true "Токен сброса"
// @Param password formData string true "Новый пароль"
// @Success 302 {string} string "Перенаправление на /auth/login"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Router /auth/reset [post]
func (h *AuthHandler) ResetPassword(c *gin.Context) {
	var input ResetInput
	errs := validation.FieldErrors{}
	if err := c.ShouldBind(&input); err != nil {
		errs.Add("password", validation.ValidateString("Пароль", input.Password, 6, 128))
		if !errs.HasErrors() {
			errs.Add("form", fmt.Errorf("некорректные данные: %v", err))
		}
		render.Page(c, http.StatusBadRequest, "auth-reset.html", gin.H{
			"Title":     "Сброс пароля",
			"ResetCode": c.PostForm("reset_code"),
			"Errors":    errs,
			"Error":     errs.Error(),
			"csrf":      csrf.GetToken(c),
		})
		return
	}

	userID := h.passwordResetSvc.GetUserIDByResetCode(c.Request.Context(), input.ResetCode)

	if err := validation.ValidatePasswordStrength(input.Password); err != nil {
		render.Page(c, http.StatusBadRequest, "auth-reset.html", gin.H{
			"Title":     "Сброс пароля",
			"ResetCode": input.ResetCode,
			"Errors":    validation.FieldErrors{"password": err.Error()},
			"Error":     err.Error(),
			"csrf":      csrf.GetToken(c),
		})
		return
	}

	if err := h.passwordResetSvc.ResetPassword(c.Request.Context(), input.ResetCode, input.Password); err != nil {
		log.Warn().Err(err).Msg("ResetPassword: failed")
		render.Page(c, http.StatusBadRequest, "auth-reset.html", gin.H{
			"Title":     "Сброс пароля",
			"ResetCode": input.ResetCode,
			"Errors":    validation.FieldErrors{},
			"Error":     "Не удалось сбросить пароль. Проверьте ссылку и попробуйте снова.",
			"csrf":      csrf.GetToken(c),
		})
		return
	}

	if userID != 0 {
		// Revoke old JWT via JTI blacklist
		if jwtCookie, jwtErr := c.Cookie("jwt"); jwtErr == nil && jwtCookie != "" {
			h.authSvc.RevokeJWT(c.Request.Context(), jwtCookie)
		}
		if err := h.authSvc.RevokeAllUserTokens(c.Request.Context(), userID); err != nil {
			log.Error().Err(err).Uint("user_id", userID).Msg("ResetPassword: failed to revoke refresh tokens")
		}
		// Clear JWT cookie after password reset to force re-login
		setSecureCookie(c, "jwt", "", -1, "/")
		setSecureCookie(c, "refresh_token", "", -1, "/auth/refresh")
	}

	if userID != 0 {
		if h.emailSvc != nil {
			if user, err := h.userService.GetByID(c.Request.Context(), userID); err == nil {
				// Через глобальную очередь (C6): раньше была untracked goroutine
				// с ручным таймаутом — очередь Enqueue уже асинхронна и не блокирует
				// ответ, а воркер отслеживается при graceful shutdown.
				if enqErr := email.Enqueue(
					user.Email,
					"Ваш пароль был изменён",
					fmt.Sprintf("Здравствуйте, %s!\n\nВаш пароль был успешно изменён. Если это были не вы, немедленно свяжитесь с поддержкой.\n\nС уважением,\nКоманда Gengine", user.Name),
				); enqErr != nil {
					log.Error().Err(enqErr).Uint("user_id", userID).Msg("ResetPassword: failed to enqueue password changed email")
				}
			}
		}
	}

	c.Redirect(http.StatusFound, "/auth/login")
}

// VerifyEmail подтверждает email пользователя.
// @Summary Подтверждение email
// @Description Активирует email пользователя по коду из письма
// @Tags auth
// @Produce html
// @Param code formData string true "Код подтверждения"
// @Success 200 {string} html "Страница подтверждения"
// @Failure 400 {object} map[string]interface{} "Неверный или просроченный код"
// @Router /auth/verify [post]
func (h *AuthHandler) VerifyEmail(c *gin.Context) {
	var req VerifyEmailRequest
	if err := c.ShouldBind(&req); err != nil {
		render.Page(c, http.StatusBadRequest, "auth-verify_error.html", gin.H{
			"Title": render.Tr(c, "generic.error"),
			"Error": render.Tr(c, "handler.verify_code_invalid"),
		})
		return
	}

	if _, err := h.emailVerificationSvc.VerifyByCode(c.Request.Context(), req.Code); err != nil {
		render.Page(c, http.StatusBadRequest, "auth-verify_error.html", gin.H{
			"Title": render.Tr(c, "generic.error"),
			"Error": render.LocalizeError(c, err.Error()),
		})
		return
	}
	render.Page(c, http.StatusOK, "auth-verify_success.html", gin.H{
		"Title": render.Tr(c, "handler.email_verified"),
	})
}

// OAuthLogin перенаправляет на страницу OAuth-провайдера.
// @Summary Начало OAuth-авторизации
// @Description Перенаправляет на страницу авторизации OAuth-провайдера (VK, Yandex)
// @Tags auth
// @Param provider path string true "Провайдер (vk, yandex)"
// @Success 302 {string} string "Перенаправление на OAuth-провайдера"
// @Failure 400 {object} map[string]interface{} "Неизвестный провайдер"
// @Router /auth/oauth/{provider} [get]
func (h *AuthHandler) OAuthLogin(c *gin.Context) {
	var req OAuthProviderRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_provider"))
		return
	}

	url, state, err := h.oauthSvc.GetAuthURL(req.Provider)
	if err != nil {
		render.RenderError(c, http.StatusBadRequest, render.LocalizeError(c, err.Error()))
		return
	}

	session := sessions.Default(c)
	session.Set("oauth_state", state)
	session.Set("oauth_state_created", time.Now().Unix())
	// NOTE: Session fixation is mitigated because auth uses JWT cookies, not session data.
	// After OAuth callback, the old session is cleared (oauth_state deleted) and a new
	// session is created implicitly on the next request to the redirect target.
	if err := session.Save(); err != nil {
		log.Error().Err(err).Msg("OAuthLogin: failed to save session")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	c.Redirect(http.StatusFound, url)
}

// OAuthCallback обрабатывает callback от OAuth-провайдера.
// @Summary Обработка callback OAuth
// @Description Завершает OAuth-авторизацию, создаёт или связывает пользователя
// @Tags auth
// @Param provider path string true "Провайдер (vk, yandex)"
// @Param code query string true "Код авторизации"
// @Param state query string true "State-параметр"
// @Success 302 {string} string "Перенаправление на /dashboard"
// @Failure 400 {object} map[string]interface{} "Ошибка авторизации"
// @Router /auth/oauth/{provider}/callback [get]
func (h *AuthHandler) OAuthCallback(c *gin.Context) {
	var req OAuthProviderRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.Page(c, http.StatusBadRequest, "auth-login.html", gin.H{
			"Title": render.Tr(c, "auth.login_title"),
			"Error": render.Tr(c, "handler.invalid_provider"),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	code := c.Query("code")
	state := c.Query("state")
	if code == "" {
		render.Page(c, http.StatusBadRequest, "auth-login.html", gin.H{
			"Title": render.Tr(c, "auth.login_title"),
			"Error": render.Tr(c, "handler.missing_code"),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	session := sessions.Default(c)
	savedState := session.Get("oauth_state")
	savedStateStr, ok := savedState.(string)
	if !ok || subtle.ConstantTimeCompare([]byte(savedStateStr), []byte(state)) != 1 {
		log.Warn().Str("provider", req.Provider).Str("state", state).Msg("OAuthCallback: state mismatch")
		render.Page(c, http.StatusBadRequest, "auth-login.html", gin.H{
			"Title": render.Tr(c, "auth.login_title"),
			"Error": render.Tr(c, "handler.state_mismatch"),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	// Check OAuth state expiry — require the created timestamp to exist
	stateCreatedVal := session.Get("oauth_state_created")
	stateCreated, ok := stateCreatedVal.(int64)
	if !ok || time.Now().Unix()-stateCreated > 600 { // 10 minutes
		log.Warn().Interface("state_created", stateCreatedVal).Msg("OAuthCallback: state expiry missing or expired")
		render.RenderErrorPage(c, http.StatusBadRequest)
		return
	}

	session.Delete("oauth_state")
	session.Delete("oauth_state_created")
	if err := session.Save(); err != nil {
		log.Error().Err(err).Msg("OAuthCallback: failed to clear session")
	}

	user, err := h.oauthSvc.Authenticate(c.Request.Context(), req.Provider, code, state)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			render.Page(c, http.StatusBadRequest, "auth-login.html", gin.H{
				"Title": render.Tr(c, "auth.login_title"),
				"Error": render.Tr(c, "handler.user_not_found"),
				"csrf":  csrf.GetToken(c),
			})
		} else {
			render.Page(c, http.StatusBadRequest, "auth-login.html", gin.H{
				"Title": render.Tr(c, "auth.login_title"),
				"Error": render.Trf(c, "handler.login_error_with", req.Provider),
				"csrf":  csrf.GetToken(c),
			})
		}
		return
	}

	// 2FA: если у пользователя включена двухфакторная аутентификация — не выдаём
	// токены сразу, а перенаправляем на ввод TOTP (защита от обхода 2FA через OAuth).
	if user.TwoFactorEnabled {
		sess := sessions.Default(c)
		sess.Set("pending_user_id", user.ID)
		sess.Set("pending_email", user.Email)
		if saveErr := sess.Save(); saveErr != nil {
			log.Error().Err(saveErr).Msg("OAuthCallback: failed to save 2FA pending session")
		}
		c.Redirect(http.StatusFound, "/auth/2fa/login?return_url=/dashboard")
		return
	}

	token, err := h.authSvc.GenerateJWT(*user)
	if err != nil {
		render.Page(c, http.StatusInternalServerError, "auth-login.html", gin.H{
			"Title": render.Tr(c, "auth.login_title"),
			"Error": render.Tr(c, "handler.internal_error"),
			"csrf":  csrf.GetToken(c),
		})
		return
	}
	setSecureCookie(c, "jwt", token, int(h.cfg.JWT.AccessExpiry.Seconds()), "/")

	deviceID := c.GetHeader("X-Device-ID")
	refreshToken, err := h.authSvc.GenerateRefreshToken(c.Request.Context(), *user, deviceID, clientFingerprint(c))
	if err == nil {
		setSecureCookie(c, "refresh_token", refreshToken, int(h.cfg.JWT.RefreshExpiry.Seconds()), "/auth/refresh")
	} else {
		log.Error().Err(err).Msg("OAuthCallback: failed to generate refresh token")
	}

	c.Redirect(http.StatusFound, "/dashboard")
}
