package csrf

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	gocsrf "github.com/gorilla/csrf"
)

const tokenKey = "_csrfToken"

func GetToken(c *gin.Context) string {
	if token, ok := c.Get(tokenKey); ok {
		if s, ok := token.(string); ok {
			return s
		}
	}
	return ""
}

func Middleware(secret string, secure bool, trustedOrigins []string) gin.HandlerFunc {
	opts := []gocsrf.Option{
		gocsrf.Secure(secure),
		gocsrf.Path("/"),
		gocsrf.MaxAge(86400),
		gocsrf.FieldName("_csrf"),
		gocsrf.CookieName("_csrf_token"),
		gocsrf.SameSite(gocsrf.SameSiteStrictMode),
		gocsrf.ErrorHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "CSRF token mismatch", http.StatusForbidden)
		})),
	}
	if len(trustedOrigins) > 0 {
		opts = append(opts, gocsrf.TrustedOrigins(trustedOrigins))
	}
	protector := gocsrf.Protect([]byte(secret), opts...)

	return func(c *gin.Context) {
		var handled bool

		// Парсим форму только для запросов с form Content-Type.
		// Без этого gin.ShouldBind не видит поля формы, т.к. gorilla/csrf
		// через PlaintextHTTPRequest создаёт копию запроса и парсит форму на ней.
		ct := c.Request.Header.Get("Content-Type")
		if strings.HasPrefix(ct, "application/x-www-form-urlencoded") ||
			strings.HasPrefix(ct, "multipart/form-data") {
			_ = c.Request.ParseMultipartForm(32 << 20) // 32MB max memory
		}

		r := c.Request
		if !secure {
			r = gocsrf.PlaintextHTTPRequest(r)
		}
		protector(http.HandlerFunc(func(w http.ResponseWriter, r2 *http.Request) {
			handled = true
			if token := gocsrf.Token(r2); token != "" {
				c.Set(tokenKey, token)
			}
			c.Next()
		})).ServeHTTP(c.Writer, r)
		if !handled {
			c.Abort()
		}
	}
}
