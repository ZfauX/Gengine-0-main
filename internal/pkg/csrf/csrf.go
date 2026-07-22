package csrf

import (
	"net/http"

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

func Middleware(secret string, secure bool) gin.HandlerFunc {
	protector := gocsrf.Protect([]byte(secret),
		gocsrf.Secure(secure),
		gocsrf.ErrorHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "CSRF token mismatch", http.StatusForbidden)
		})),
	)

	return func(c *gin.Context) {
		var handled bool
		protector(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handled = true
			if token := gocsrf.Token(r); token != "" {
				c.Set(tokenKey, token)
			}
			c.Next()
		})).ServeHTTP(c.Writer, c.Request)
		if handled {
			_ = c.Request.ParseForm()
		} else {
			c.Abort()
		}
	}
}
