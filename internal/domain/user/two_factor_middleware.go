// internal/domain/user/two_factor_middleware.go
package user

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// twoFAStepUpTTL — срок действия подтверждённого 2FA step-up (15 минут).
// S14: без TTL флаг жил в cookie-сессии до logout — украденная кука давала
// бессрочный доступ к /admin/*. Step-up теперь ограничен по времени.
const twoFAStepUpTTL = 15 * time.Minute

// trustedDeviceCookie — имя cookie «запомнить это устройство» (UX-2, PASS-13).
// Содержит подписанный payload: userID:unixExpiry:hmac.
const trustedDeviceCookie = "2fa_trusted"

// trustedDeviceTTL — срок жизни trusted cookie (30 дней).
const trustedDeviceTTL = 30 * 24 * time.Hour

// session2FAKey возвращает ключ флага 2FA, привязанный к userID, чтобы флаг
// не "перетекал" между аккаунтами на одном браузере.
func session2FAKey(userID uint) string {
	return "2fa_verified_" + strconv.FormatUint(uint64(userID), 10)
}

// setTrustedDeviceCookie ставит trusted-device cookie (UX-2, PASS-13):
// payload = userID:expiryUnix, подписан HMAC-SHA256 от trustedSecret.
func setTrustedDeviceCookie(c *gin.Context, userID uint, secret string) {
	if secret == "" {
		return
	}
	expiry := time.Now().Add(trustedDeviceTTL).Unix()
	payload := fmt.Sprintf("%d:%d", userID, expiry)
	sig := hmacSHA256Hex(secret, payload)
	value := payload + ":" + sig
	// H6 (PASS-15): Secure-флаг доверенной куки соответствует JWT/refresh
	// (передаётся из cfg.Server). Раньше secure=false — кука дающая обход
	// 2FA уходила по HTTP (MITM).
	secure := trustedSecureFlag()
	c.SetCookie(trustedDeviceCookie, value, int(trustedDeviceTTL.Seconds()), "/", "", secure, true)
}

// isTrustedDevice проверяет trusted-device cookie: подпись валидна, userID
// совпадает, срок не истёк. Только если trustedSecret задан.
func isTrustedDevice(c *gin.Context, userID uint, secret string) bool {
	if secret == "" {
		return false
	}
	val, err := c.Cookie(trustedDeviceCookie)
	if err != nil || val == "" {
		return false
	}
	// payload:signature
	idx := strings.LastIndex(val, ":")
	if idx <= 0 {
		return false
	}
	payload := val[:idx]
	sig := val[idx+1:]
	if !hmacSHA256Equal(secret, payload, sig) {
		return false
	}
	parts := strings.SplitN(payload, ":", 2)
	if len(parts) != 2 {
		return false
	}
	id, err1 := strconv.ParseUint(parts[0], 10, 64)
	expUnix, err2 := strconv.ParseInt(parts[1], 10, 64)
	if err1 != nil || err2 != nil {
		return false
	}
	if uint(id) != userID {
		return false
	}
	return time.Now().Unix() < expUnix
}

// clearTrustedDeviceCookie удаляет trusted-device cookie (logout).
func clearTrustedDeviceCookie(c *gin.Context) {
	c.SetCookie(trustedDeviceCookie, "", -1, "/", "", trustedSecureFlag(), true)
}

// trustedDeviceSecret — глобальный секрет trusted-device cookie (UX-2).
// Устанавливается из routes.go через SetTrustedSecret (cfg.Session.Secret).
// Хранится глобально, т.к. TwoFactorRequired — самостоятельный middleware.
var trustedDeviceSecret string

// trustedDeviceSecure — глобальный Secure-флаг trusted-device cookie (H6, PASS-15).
var trustedDeviceSecure bool

// SetTrustedSecret регистрирует секрет для trusted-device cookie.
func SetTrustedSecret(secret string) {
	trustedDeviceSecret = secret
}

// SetTrustedSecure (H6, PASS-15): задаёт Secure-флаг trusted-device cookie.
func SetTrustedSecure(secure bool) {
	trustedDeviceSecure = secure
}

// trustedSecureFlag возвращает текущий Secure-флаг.
func trustedSecureFlag() bool {
	return trustedDeviceSecure
}

// hmacSHA256Hex возвращает hex-кодированную HMAC-SHA256 подпись payload.
func hmacSHA256Hex(secret, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// hmacSHA256Equal проверяет подпись через constant-time сравнение.
func hmacSHA256Equal(secret, payload, sig string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(sig))
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

		// UX-2 (PASS-13): «запомнить это устройство» — trusted-device cookie
		// заменяет повторный step-up на доверенных устройствах (30 дней).
		if trustedDeviceSecret != "" && isTrustedDevice(c, userIDVal, trustedDeviceSecret) {
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
