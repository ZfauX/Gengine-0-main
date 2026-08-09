// internal/domain/notification/push_client.go
// S-45-3 (pass 45): HTTP-клиент для Web Push с блокировкой приватных адресов
// на уровне соединения. Endpoint проверяется при подписке (user/push_handler.go),
// но DNS-rebinding мог переключить имя на внутренний адрес к моменту отправки —
// DialContext резолвит имя и отклоняет приватные/loopback/CGNAT IP прямо в
// момент запроса, закрывая SSRF.
package notification

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

func newPushHTTPClient() *http.Client {
	transport := &http.Transport{
		// Resolver блокирует приватные адреса ДО установки соединения.
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			if len(ips) == 0 {
				return nil, errors.New("push endpoint: no addresses found")
			}
			for _, ipa := range ips {
				if isPrivateIP(ipa.IP) {
					return nil, errPushPrivateAddress
				}
			}
			dialer := &net.Dialer{Timeout: 5 * time.Second}
			// Дозваниваемся по первому публичному адресу (SNI/TLS берётся из URL).
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
		TLSHandshakeTimeout: 5 * time.Second,
	}
	return &http.Client{Transport: transport, Timeout: 10 * time.Second}
}

// errPushPrivateAddress — endpoint резолвится в приватный адрес (SSRF).
var errPushPrivateAddress = &pushPrivateAddrError{}

type pushPrivateAddrError struct{}

func (e *pushPrivateAddrError) Error() string {
	return "push endpoint resolves to a private address"
}

func isPrivateIP(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	// CGNAT (RFC 6598) — «приватная» сеть операторов, SSRF-таргет.
	if ip.To4() != nil {
		v4 := ip.To4()
		return v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
	}
	return false
}
