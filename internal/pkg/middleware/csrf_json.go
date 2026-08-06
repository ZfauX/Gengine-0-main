// internal/pkg/middleware/csrf_json.go
package middleware

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// APIOriginGuard защищает cookie-авторизованные JSON-мутации /api/* от CSRF
// (pass 24 / S-1). SameSite=Strict на куках уже смягчает, но для defense in
// depth проверяем Origin/Sec-Fetch-Site:
//   - если заголовок Origin есть и не совпадает с host запроса → 403;
//   - если Sec-Fetch-Site есть и не same-origin/none → 403.
//
// GET/HEAD/OPTIONS пропускаются. Регистрируется на небезопасные методы
// группы /api/*.
func APIOriginGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == "GET" || c.Request.Method == "HEAD" || c.Request.Method == "OPTIONS" {
			c.Next()
			return
		}

		// Sec-Fetch-Site (современные браузеры): разрешены same-origin и none.
		if sfs := c.GetHeader("Sec-Fetch-Site"); sfs != "" && sfs != "same-origin" && sfs != "none" {
			abortCSRF(c)
			return
		}

		// Origin: если присутствует, должен совпадать с host (без порта-аномалий).
		if origin := c.GetHeader("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || !strings.EqualFold(u.Host, c.Request.Host) {
				abortCSRF(c)
				return
			}
		}

		c.Next()
	}
}

func abortCSRF(c *gin.Context) {
	log.Warn().Str("method", c.Request.Method).Str("path", c.Request.URL.Path).
		Str("origin", c.GetHeader("Origin")).Str("sec_fetch_site", c.GetHeader("Sec-Fetch-Site")).
		Msg("API: cross-origin mutating request rejected")
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
		"error": "cross-origin request forbidden",
		"code":  "csrf_origin",
	})
}
