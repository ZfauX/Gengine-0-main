// internal/pkg/middleware/trusted_proxy.go
package middleware

import (
	"net"
	"strings"

	"github.com/gin-gonic/gin"
)

// TrustedProxyMiddleware настраивает Gin на доверенные прокси,
// чтобы ClientIP() корректно читал X-Forwarded-For.
func TrustedProxyMiddleware(proxies []string) gin.HandlerFunc {
	if len(proxies) == 0 {
		return func(c *gin.Context) {
			c.Next()
		}
	}

	// Собираем список доверенных IP/CIDR
	trusted := make([]*net.IPNet, 0, len(proxies))
	for _, p := range proxies {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// Пытаемся распарсить как CIDR
		_, cidr, _ := net.ParseCIDR(p)
		if cidr != nil {
			trusted = append(trusted, cidr)
			continue
		}
		// Пытаемся распарсить как IP
		ip := net.ParseIP(p)
		if ip != nil {
			// Для одиночного IP добавляем /32 или /128
			bit := 32
			if ip.To4() == nil {
				bit = 128
			}
			_, cidr, _ := net.ParseCIDR(ip.String() + "/" + string(rune(bit)))
			if cidr != nil {
				trusted = append(trusted, cidr)
			}
		}
	}

	// Если не удалось распарсить ни одного — просто пропускаем
	if len(trusted) == 0 {
		return func(c *gin.Context) {
			c.Next()
		}
	}

	return func(c *gin.Context) {
		// Проверяем, что клиент из доверенного прокси
		clientIP := c.ClientIP()
		if ip, _, err := net.SplitHostPort(clientIP); err == nil {
			clientIP = ip
		}
		isTrusted := false
		for _, t := range trusted {
			if t.Contains(net.ParseIP(clientIP)) {
				isTrusted = true
				break
			}
		}
		if !isTrusted {
			c.Next()
			return
		}

		// Если прокси доверенный, берём IP из X-Forwarded-For
		if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			// Берём первый IP (реальный клиент)
			realIP := strings.TrimSpace(parts[0])
			if realIP != "" {
				c.Set("real_ip", realIP)
			}
		}
		c.Next()
	}
}
