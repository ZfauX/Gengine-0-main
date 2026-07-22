// internal/domain/user/auth_handler.go
package user

import (
	"errors"
	"fmt"
	"net/http"

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
	}
}

func (h *AuthHandler) ShowLoginForm(c *gin.Context) {
	render.Page(c, http.StatusOK, "auth-login.html", gin.H{
		"csrf": csrf.GetToken(c),
	})
}

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
			"Errors": errs,
			"Error":  errs.Error(),
			"csrf":   csrf.GetToken(c),
		})
		return
	}

	token, err := h.authSvc.Login(c.Request.Context(), input.Email, input.Password)
	if err != nil {
		render.Page(c, http.StatusUnauthorized, "auth-login.html", gin.H{
			"Errors": validation.FieldErrors{"email": "Неверный email или пароль"},
			"Error":  "Неверный email или пароль",
			"csrf":   csrf.GetToken(c),
		})
		return
	}

	userID, _, parseErr := h.authSvc.ParseToken(token)
	if parseErr != nil {
		log.Error().Err(parseErr).Msg("Login: failed to parse token for audit")
	} else {
		h.auditSvc.Log(userID, "login", "user", userID, input.Email)
	}

	setSecureCookie(c, "jwt", token, int(h.cfg.JWT.AccessExpiry.Seconds()), "/")

	user, err := h.userService.GetByEmail(c.Request.Context(), input.Email)
	if err == nil {
		deviceID := c.GetHeader("X-Device-ID")
		refreshToken, err := h.authSvc.GenerateRefreshToken(c.Request.Context(), *user, deviceID)
		if err == nil {
			setSecureCookie(c, "refresh_token", refreshToken, int(h.cfg.JWT.RefreshExpiry.Seconds()), "/auth/refresh")
		} else {
			log.Error().Err(err).Msg("Login: failed to generate refresh token")
		}
	}

	c.Redirect(http.StatusFound, "/dashboard")
}

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

	newAccessToken, err := h.authSvc.RefreshAccessToken(c.Request.Context(), refreshToken)
	if err != nil {
		appErr := apperrors.Unauthorized(err.Error())
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	setSecureCookie(c, "jwt", newAccessToken, int(h.cfg.JWT.AccessExpiry.Seconds()), "/")

	c.JSON(http.StatusOK, gin.H{
		"access_token": newAccessToken,
		"expires_in":   int(h.cfg.JWT.AccessExpiry.Seconds()),
	})
}

func (h *AuthHandler) Logout(c *gin.Context) {
	refreshTokenCookie, err := c.Cookie("refresh_token")
	if err == nil && refreshTokenCookie != "" {
		if err := h.authSvc.RevokeRefreshToken(c.Request.Context(), refreshTokenCookie); err != nil {
			log.Warn().Err(err).Msg("Logout: failed to revoke refresh token")
		}
	}
	setSecureCookie(c, "jwt", "", -1, "/")
	setSecureCookie(c, "refresh_token", "", -1, "/auth/refresh")
	c.Redirect(http.StatusFound, "/")
}

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
	c.Redirect(http.StatusFound, "/")
}

func (h *AuthHandler) ShowRegisterForm(c *gin.Context) {
	render.Page(c, http.StatusOK, "auth-register.html", gin.H{
		"csrf": csrf.GetToken(c),
	})
}

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
			"Errors": errs,
			"Error":  errs.Error(),
			"csrf":   csrf.GetToken(c),
		})
		return
	}

	cleanName := sanitize.StripHTML(input.Name)
	cleanEmail := sanitize.StripHTML(input.Email)

	user, err := h.authSvc.Register(c.Request.Context(), cleanEmail, input.Password, cleanName)
	if err != nil {
		render.Page(c, http.StatusConflict, "auth-register.html", gin.H{
			"Errors": validation.FieldErrors{"email": "Email уже зарегистрирован"},
			"Error":  "Email уже зарегистрирован",
			"csrf":   csrf.GetToken(c),
		})
		return
	}

	h.auditSvc.Log(user.ID, "register", "user", user.ID, cleanEmail)
	c.Redirect(http.StatusFound, "/auth/login")
}

func (h *AuthHandler) ShowForgotForm(c *gin.Context) {
	render.Page(c, http.StatusOK, "auth-forgot.html", gin.H{
		"csrf": csrf.GetToken(c),
	})
}

func (h *AuthHandler) ForgotPassword(c *gin.Context) {
	var input ForgotInput
	errs := validation.FieldErrors{}
	if err := c.ShouldBind(&input); err != nil {
		errs.Add("email", validation.ValidateString("Email", input.Email, 1, 255))
		if !errs.HasErrors() {
			errs.Add("email", fmt.Errorf("некорректный email"))
		}
		render.Page(c, http.StatusBadRequest, "auth-forgot.html", gin.H{
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
		if _, err := h.passwordResetSvc.GenerateToken(c.Request.Context(), *user); err != nil {
			log.Error().Err(err).Str("email", input.Email).Msg("ForgotPassword: failed to generate token")
		}
	}

	message := "Инструкции отправлены на почту"
	if !h.cfg.SMTP.Enabled {
		message = "Функция восстановления пароля временно недоступна"
	}

	render.Page(c, http.StatusOK, "auth-forgot.html", gin.H{
		"Message": message,
		"csrf":    csrf.GetToken(c),
	})
}

func (h *AuthHandler) ShowResetForm(c *gin.Context) {
	resetCode := sanitize.StripHTML(c.Param("resetCode"))
	if resetCode == "" {
		render.RenderErrorPage(c, http.StatusBadRequest)
		return
	}
	if _, err := h.passwordResetSvc.passResetRepo.GetTokenByResetCode(c.Request.Context(), resetCode); err != nil {
		render.Page(c, http.StatusBadRequest, "auth-reset.html", gin.H{
			"Error": "Недействительная или истёкшая ссылка для сброса пароля",
			"csrf":  csrf.GetToken(c),
		})
		return
	}
	render.Page(c, http.StatusOK, "auth-reset.html", gin.H{
		"ResetCode": resetCode,
		"csrf":      csrf.GetToken(c),
	})
}

func (h *AuthHandler) ResetPassword(c *gin.Context) {
	var input ResetInput
	errs := validation.FieldErrors{}
	if err := c.ShouldBind(&input); err != nil {
		errs.Add("password", validation.ValidateString("Пароль", input.Password, 6, 128))
		if !errs.HasErrors() {
			errs.Add("form", fmt.Errorf("некорректные данные: %v", err))
		}
		render.Page(c, http.StatusBadRequest, "auth-reset.html", gin.H{
			"ResetCode": c.PostForm("reset_code"),
			"Errors":    errs,
			"Error":     errs.Error(),
			"csrf":      csrf.GetToken(c),
		})
		return
	}

	var userID uint
	token, err := h.passwordResetSvc.passResetRepo.GetTokenByResetCode(c.Request.Context(), input.ResetCode)
	if err == nil {
		userID = token.UserID
	}

	if err := h.passwordResetSvc.ResetPassword(c.Request.Context(), input.ResetCode, input.Password); err != nil {
		errs.Add("password", err)
		if !errs.HasErrors() {
			errs.Add("token", err)
		}
		render.Page(c, http.StatusBadRequest, "auth-reset.html", gin.H{
			"ResetCode": input.ResetCode,
			"Errors":    errs,
			"Error":     errs.Error(),
			"csrf":      csrf.GetToken(c),
		})
		return
	}

	if userID != 0 {
		if err := h.authSvc.RevokeAllUserTokens(c.Request.Context(), userID); err != nil {
			log.Error().Err(err).Uint("user_id", userID).Msg("ResetPassword: failed to revoke refresh tokens")
		}
	}

	if userID != 0 {
		if h.emailSvc != nil {
			if user, err := h.userService.GetByID(c.Request.Context(), userID); err == nil {
				go func() {
					if err := h.emailSvc.SendPasswordChangedEmail(user.Email, user.Name); err != nil {
						log.Error().Err(err).Uint("user_id", userID).Msg("ResetPassword: failed to send password changed email")
					}
				}()
			}
		}
	}

	c.Redirect(http.StatusFound, "/auth/login")
}

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

func (h *AuthHandler) OAuthLogin(c *gin.Context) {
	var req OAuthProviderRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверный провайдер")
		return
	}

	url, state, err := h.oauthSvc.GetAuthURL(req.Provider)
	if err != nil {
		render.RenderError(c, http.StatusBadRequest, err.Error())
		return
	}

	session := sessions.Default(c)
	session.Set("oauth_state", state)
	if err := session.Save(); err != nil {
		log.Error().Err(err).Msg("OAuthLogin: failed to save session")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	c.Redirect(http.StatusFound, url)
}

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

	session := sessions.Default(c)
	savedState := session.Get("oauth_state")
	if savedState == nil || savedState != state {
		log.Warn().Str("provider", req.Provider).Str("state", state).Msg("OAuthCallback: state mismatch")
		render.Page(c, http.StatusBadRequest, "auth-login.html", gin.H{
			"Error": "Ошибка авторизации: неверный параметр state",
			"csrf":  csrf.GetToken(c),
		})
		return
	}
	session.Delete("oauth_state")
	if err := session.Save(); err != nil {
		log.Error().Err(err).Msg("OAuthCallback: failed to clear session")
	}

	user, err := h.oauthSvc.Authenticate(c.Request.Context(), req.Provider, code, state)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			render.Page(c, http.StatusBadRequest, "auth-login.html", gin.H{
				"Error": "Пользователь не найден",
				"csrf":  csrf.GetToken(c),
			})
		} else {
			render.Page(c, http.StatusBadRequest, "auth-login.html", gin.H{
				"Error": "Ошибка входа через " + req.Provider,
				"csrf":  csrf.GetToken(c),
			})
		}
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
	setSecureCookie(c, "jwt", token, int(h.cfg.JWT.AccessExpiry.Seconds()), "/")

	deviceID := c.GetHeader("X-Device-ID")
	refreshToken, err := h.authSvc.GenerateRefreshToken(c.Request.Context(), *user, deviceID)
	if err == nil {
		setSecureCookie(c, "refresh_token", refreshToken, int(h.cfg.JWT.RefreshExpiry.Seconds()), "/auth/refresh")
	} else {
		log.Error().Err(err).Msg("OAuthCallback: failed to generate refresh token")
	}

	c.Redirect(http.StatusFound, "/dashboard")
}
