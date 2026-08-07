// internal/domain/user/two_factor_middleware.go
package user

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// twoFAStepUpTTL — срок действия подтверждённого 2FA step-up (15 минут).
// S14: без TTL флаг жил в cookie-сессии до logout — украденная кука давала
// бессрочный доступ к /admin/*. Step-up теперь ограничен по времени.
const twoFAStepUpTTL = 15 * time.Minute

// session2FAKey возвращает ключ флага 2FA, привязанный к userID, чтобы флаг
// не "перетекал" между аккаунтами на одном браузере.
func session2FAKey(userID uint) string {
	return "2fa_verified_" + strconv.FormatUint(uint64(userID), 10)
}

// is2FAVerified проверяет, что step-up 2FA подтверждён и не истёк.
// Принимает legacy bool (устаревшие сессии требуют повторного подтверждения)
// и int64 timestamp, записанный через set2FAVerified.
func is2FAVerified(session sessions.Session, userID uint) bool {
	v := session.Get(session2FAKey(userID))
	switch val := v.(type) {
	case bool:
		// legacy true — нет времени верификации, считаем недействительным,
		// чтобы старые куки не давали бессрочного доступа.
		return false
	case int64:
		return time.Now().Unix()-val < int64(twoFAStepUpTTL.Seconds())
	default:
		return false
	}
}

// set2FAVerified записывает метку времени подтверждённого 2FA.
func set2FAVerified(session sessions.Session, userID uint) {
	session.Set(session2FAKey(userID), time.Now().Unix())
}

// clear2FASessionFlag удаляет флаг верификации 2FA из сессии (при logout/disable/reset).
func clear2FASessionFlag(c *gin.Context) {
	session := sessions.Default(c)
	for _, key := range []string{
		session2FAKey(c.GetUint("userID")),
		"2fa_verified", // legacy key
	} {
		session.Delete(key)
	}
	if err := session.Save(); err != nil {
		log.Warn().Err(err).Msg("clear2FASessionFlag: failed to save session")
	}
}

// TwoFactorRequired проверяет, что у пользователя включена 2FA и он прошёл проверку.
// Используется для защиты чувствительных маршрутов (admin, profile, 2FA settings).
// Флаг верификации персистируется в сессии, чтобы выдерживать multiple запросы.
func TwoFactorRequired(twoFactorSvc *TwoFactorService, userRepo UserRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, exists := c.Get("userID")
		if !exists {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		userIDVal, ok := userID.(uint)
		if !ok {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		// Проверяем, что пользователь прошёл проверку 2FA в этой сессии (флаг привязан к userID)
		session := sessions.Default(c)
		if is2FAVerified(session, userIDVal) {
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

		// 2FA включена, но не верифицирована — перенаправляем на /auth/2fa/verify
		returnURL := url.URL{Path: c.Request.URL.Path}
		if c.Request.URL.RawQuery != "" {
			returnURL.RawQuery = c.Request.URL.RawQuery
		}
		redirectURL := fmt.Sprintf("/auth/2fa/verify?return_url=%s", url.QueryEscape(returnURL.String()))
		c.Redirect(http.StatusFound, redirectURL)
		c.Abort()
	}
}

// TwoFactorBackupCodeRequired проверяет резервный код и перенаправляет на /auth/2fa/backup.
func TwoFactorBackupCodeRequired(twoFactorSvc *TwoFactorService, userRepo UserRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, exists := c.Get("userID")
		if !exists {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		userIDVal, ok := userID.(uint)
		if !ok {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		// Проверяем сессию — если уже верифицирован (флаг привязан к userID), пропускаем
		session := sessions.Default(c)
		if is2FAVerified(session, userIDVal) {
			c.Next()
			return
		}

		// Получаем пользователя для проверки статуса 2FA
		userObj, err := userRepo.GetByID(c.Request.Context(), userIDVal)
		if err != nil {
			log.Error().Err(err).Uint("user_id", userIDVal).Msg("TwoFactorBackupCodeRequired: failed to get user")
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		// Если 2FA не включена — пропускаем (флаг не ставим, проверка по userObj актуальна каждый раз)
		if !userObj.TwoFactorEnabled {
			c.Next()
			return
		}

		// Перенаправляем на страницу ввода кода
		c.Redirect(http.StatusFound, "/auth/2fa/backup")
		c.Abort()
	}
}
