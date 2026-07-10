// internal/domain/user/two_factor_middleware.go
package user

import (
	"net/http"
	"strings"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

const sessionKey2FAMiddlewareVerified = "2fa_verified"

// TwoFactorRequired проверяет, что у пользователя включена 2FA и он прошёл проверку.
// Используется для защиты админ-маршрутов.
// Флаг верификации персистируется в сессии, чтобы выдерживать multiple запросы.
func TwoFactorRequired(twoFactorSvc *TwoFactorService, userRepo UserRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, exists := c.Get("userID")
		if !exists {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		userIDVal := userID.(uint)

		// Проверяем, что пользователь прошёл проверку 2FA в этой сессии
		session := sessions.Default(c)
		if session.Get(sessionKey2FAMiddlewareVerified) == true {
			c.Next()
			return
		}

		// Получаем пользователя
		userObj, err := userRepo.GetByID(c.Request.Context(), userIDVal)
		if err != nil {
			log.Error().Err(err).Uint("user_id", userIDVal).Msg("TwoFactorRequired: failed to get user")
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		// Если 2FA не включена — пропускаем
		if !userObj.TwoFactorEnabled {
			c.Next()
			return
		}

		// Проверяем, что это запрос подтверждения кода
		code := c.Query("code")
		if code == "" {
			code = c.PostForm("code")
		}

		// Если код не передан — перенаправляем на страницу ввода
		if code == "" {
			// Сохраняем return URL для редиректа после проверки
			returnURL := c.Request.URL.String()
			if strings.Contains(returnURL, "?") {
				returnURL += "&redirect=1"
			} else {
				returnURL += "?redirect=1"
			}

			c.HTML(http.StatusOK, "admin-2fa-verify.html", gin.H{
				"Title":     "Подтверждение 2FA",
				"Message":   "Введите код из Google Authenticator",
				"ReturnURL": returnURL,
			})
			c.Abort()
			return
		}

		// Проверяем TOTP-код
		valid, err := twoFactorSvc.VerifyCode(userObj.TwoFactorSecret, code)
		if err != nil {
			log.Error().Err(err).Uint("user_id", userIDVal).Msg("TwoFactorRequired: TOTP verification error")
			returnURL := c.Request.URL.String()
			if strings.Contains(returnURL, "?") {
				returnURL += "&redirect=1"
			} else {
				returnURL += "?redirect=1"
			}
			c.HTML(http.StatusOK, "admin-2fa-verify.html", gin.H{
				"Title":     "Подтверждение 2FA",
				"Error":     "Ошибка проверки кода",
				"ReturnURL": returnURL,
			})
			c.Abort()
			return
		}

		if !valid {
			returnURL := c.Request.URL.String()
			if strings.Contains(returnURL, "?") {
				returnURL += "&redirect=1"
			} else {
				returnURL += "?redirect=1"
			}
			c.HTML(http.StatusOK, "admin-2fa-verify.html", gin.H{
				"Title":     "Подтверждение 2FA",
				"Error":     "Неверный код",
				"ReturnURL": returnURL,
			})
			c.Abort()
			return
		}

		// Сохраняем флаг верификации в сессии (персистируется между запросами)
		session.Set(sessionKey2FAMiddlewareVerified, true)
		_ = session.Save()
		c.Next()
	}
}

// TwoFactorBackupCodeRequired проверяет резервный код.
// Используется когда у пользователя нет доступа к TOTP-генератору.
// Флаг верификации персистируется в сессии.
func TwoFactorBackupCodeRequired(twoFactorSvc *TwoFactorService, userRepo UserRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, exists := c.Get("userID")
		if !exists {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		userIDVal := userID.(uint)

		// Проверяем сессию — если уже верифицирован, пропускаем
		session := sessions.Default(c)
		if session.Get(sessionKey2FAMiddlewareVerified) == true {
			c.Next()
			return
		}

		backupCode := c.PostForm("backup_code")
		if backupCode == "" {
			c.HTML(http.StatusOK, "admin-2fa-backup.html", gin.H{
				"Title": "Резервный код 2FA",
				"Error": "Введите резервный код",
			})
			c.Abort()
			return
		}

		userObj, err := userRepo.GetByID(c.Request.Context(), userIDVal)
		if err != nil {
			c.HTML(http.StatusOK, "admin-2fa-backup.html", gin.H{
				"Title": "Резервный код 2FA",
				"Error": "Ошибка загрузки пользователя",
			})
			c.Abort()
			return
		}

		if !userObj.TwoFactorEnabled {
			session.Set(sessionKey2FAMiddlewareVerified, true)
			_ = session.Save()
			c.Next()
			return
		}

		valid, err := twoFactorSvc.VerifyBackupCode(userObj.TwoFactorBackupCodes, backupCode)
		if err != nil {
			c.HTML(http.StatusOK, "admin-2fa-backup.html", gin.H{
				"Title": "Резервный код 2FA",
				"Error": "Ошибка проверки кода",
			})
			c.Abort()
			return
		}

		if !valid {
			c.HTML(http.StatusOK, "admin-2fa-backup.html", gin.H{
				"Title": "Резервный код 2FA",
				"Error": "Неверный резервный код",
			})
			c.Abort()
			return
		}

		session.Set(sessionKey2FAMiddlewareVerified, true)
		_ = session.Save()
		c.Next()
	}
}
