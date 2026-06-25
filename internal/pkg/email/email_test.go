// internal/pkg/email/email_test.go
package email

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"gengine-0/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Вспомогательный тестовый SMTP-сервер
// =============================================================================

// testSMTPServer — простой SMTP-сервер для тестов, работает в памяти.
type testSMTPServer struct {
	addr     string
	listener net.Listener
	// Хранит последнее полученное письмо
	lastMail struct {
		from    string
		to      string
		data    string
		headers map[string]string
		body    string
	}
	mu sync.Mutex
	// Канал для уведомления о получении письма
	mailReceived chan struct{}
	// Флаги для эмуляции ошибок
	failAuth     bool
	failMailFrom bool
	failRcptTo   bool
	failData     bool
	failQuit     bool
	started      bool
	shutdown     chan struct{}
}

// newTestSMTPServer создаёт и запускает тестовый SMTP-сервер на случайном порту.
func newTestSMTPServer(t *testing.T) *testSMTPServer {
	t.Helper()
	s := &testSMTPServer{
		mailReceived: make(chan struct{}, 1),
		shutdown:     make(chan struct{}),
	}
	var err error
	s.listener, err = net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s.addr = s.listener.Addr().String()
	s.started = true

	go s.serve(t)
	time.Sleep(50 * time.Millisecond)
	return s
}

func (s *testSMTPServer) serve(t *testing.T) {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.shutdown:
				return
			default:
				t.Logf("accept error: %v", err)
				continue
			}
		}
		go s.handleConn(t, conn)
	}
}

func (s *testSMTPServer) handleConn(t *testing.T, conn net.Conn) {
	defer func() {
		if err := conn.Close(); err != nil {
			t.Logf("error closing connection: %v", err)
		}
	}()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	// Используем fmt.Fprintf с проверкой ошибки
	if err := s.writeResponse(writer, 220, "localhost test SMTP"); err != nil {
		t.Logf("write response error: %v", err)
		return
	}
	if err := writer.Flush(); err != nil {
		return
	}

	var from, to string

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSuffix(line, "\r\n")
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, " ", 2)
		cmd := strings.ToUpper(parts[0])
		arg := ""
		if len(parts) > 1 {
			arg = parts[1]
		}

		switch cmd {
		case "HELO", "EHLO":
			if err := s.writeResponse(writer, 250, "localhost"); err != nil {
				return
			}
			if err := writer.Flush(); err != nil {
				return
			}
		case "MAIL":
			if s.failMailFrom {
				if err := s.writeResponse(writer, 550, "mail from failed"); err != nil {
					return
				}
				if err := writer.Flush(); err != nil {
					return
				}
				continue
			}
			from = strings.TrimPrefix(arg, "FROM:")
			from = strings.Trim(from, "<>")
			if err := s.writeResponse(writer, 250, "OK"); err != nil {
				return
			}
			if err := writer.Flush(); err != nil {
				return
			}
		case "RCPT":
			if s.failRcptTo {
				if err := s.writeResponse(writer, 550, "rcpt to failed"); err != nil {
					return
				}
				if err := writer.Flush(); err != nil {
					return
				}
				continue
			}
			to = strings.TrimPrefix(arg, "TO:")
			to = strings.Trim(to, "<>")
			if err := s.writeResponse(writer, 250, "OK"); err != nil {
				return
			}
			if err := writer.Flush(); err != nil {
				return
			}
		case "DATA":
			if s.failData {
				if err := s.writeResponse(writer, 550, "data failed"); err != nil {
					return
				}
				if err := writer.Flush(); err != nil {
					return
				}
				continue
			}
			if err := s.writeResponse(writer, 354, "Start mail input; end with <CRLF>.<CRLF>"); err != nil {
				return
			}
			if err := writer.Flush(); err != nil {
				return
			}
			var mailData strings.Builder
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if line == ".\r\n" || line == ".\n" {
					break
				}
				mailData.WriteString(line)
			}
			data := mailData.String()
			headers := make(map[string]string)
			body := ""
			lines := strings.Split(data, "\n")
			isHeader := true
			for _, line := range lines {
				line = strings.TrimSuffix(line, "\r")
				if isHeader && line == "" {
					isHeader = false
					continue
				}
				if isHeader {
					if idx := strings.Index(line, ":"); idx > 0 {
						key := strings.TrimSpace(line[:idx])
						val := strings.TrimSpace(line[idx+1:])
						headers[key] = val
					}
				} else {
					body += line + "\n"
				}
			}
			body = strings.TrimSuffix(body, "\n")

			s.mu.Lock()
			s.lastMail.from = from
			s.lastMail.to = to
			s.lastMail.data = data
			s.lastMail.headers = headers
			s.lastMail.body = body
			s.mu.Unlock()

			select {
			case s.mailReceived <- struct{}{}:
			default:
			}

			if err := s.writeResponse(writer, 250, "OK"); err != nil {
				return
			}
			if err := writer.Flush(); err != nil {
				return
			}
		case "QUIT":
			if s.failQuit {
				if err := s.writeResponse(writer, 500, "quit failed"); err != nil {
					return
				}
				if err := writer.Flush(); err != nil {
					return
				}
				continue
			}
			if err := s.writeResponse(writer, 221, "Bye"); err != nil {
				return
			}
			if err := writer.Flush(); err != nil {
				return
			}
			return
		case "AUTH":
			if s.failAuth {
				if err := s.writeResponse(writer, 535, "auth failed"); err != nil {
					return
				}
				if err := writer.Flush(); err != nil {
					return
				}
				continue
			}
			if err := s.writeResponse(writer, 235, "Authentication successful"); err != nil {
				return
			}
			if err := writer.Flush(); err != nil {
				return
			}
		default:
			if err := s.writeResponse(writer, 502, "Command not implemented"); err != nil {
				return
			}
			if err := writer.Flush(); err != nil {
				return
			}
		}
	}
}

// writeResponse теперь возвращает ошибку, чтобы её можно было проверить.
func (s *testSMTPServer) writeResponse(w *bufio.Writer, code int, text string) error {
	if text == "" {
		_, err := fmt.Fprintf(w, "%d \r\n", code)
		return err
	}
	_, err := fmt.Fprintf(w, "%d %s\r\n", code, text)
	return err
}

func (s *testSMTPServer) Close() {
	if s.listener != nil {
		_ = s.listener.Close()
	}
	close(s.shutdown)
	s.started = false
}

func (s *testSMTPServer) LastMail() (from, to, body string, headers map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastMail.from, s.lastMail.to, s.lastMail.body, s.lastMail.headers
}

func (s *testSMTPServer) WaitForMail(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-s.mailReceived:
		return
	case <-time.After(timeout):
		t.Fatal("timeout waiting for mail")
	}
}

// =============================================================================
// Тесты
// =============================================================================

func TestSendEmail_Success(t *testing.T) {
	server := newTestSMTPServer(t)
	defer server.Close()

	hostParts := strings.Split(server.addr, ":")
	host := hostParts[0]
	var port int
	_, err := fmt.Sscanf(hostParts[1], "%d", &port)
	require.NoError(t, err)

	cfg := &config.Config{
		SMTP: config.SMTPConfig{
			Enabled:  true,
			Host:     host,
			Port:     port,
			User:     "",
			Password: "",
			From:     "sender@test.com",
		},
	}

	to := "receiver@test.com"
	subject := "Test Subject"
	body := "Hello, this is a test email."

	err = SendEmail(cfg, to, subject, body)
	require.NoError(t, err)

	server.WaitForMail(t, 2*time.Second)

	from, toActual, bodyActual, headers := server.LastMail()
	assert.Equal(t, "sender@test.com", from)
	assert.Equal(t, "receiver@test.com", toActual)
	assert.Contains(t, bodyActual, body)
	assert.Equal(t, "Test Subject", headers["Subject"])
	assert.Equal(t, "sender@test.com", headers["From"])
	assert.Equal(t, "receiver@test.com", headers["To"])
}

func TestSendEmail_SMTPDisabled(t *testing.T) {
	cfg := &config.Config{
		SMTP: config.SMTPConfig{Enabled: false},
	}
	err := SendEmail(cfg, "to@test.com", "subj", "body")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SMTP is not enabled")
}

func TestSendEmail_InvalidHost(t *testing.T) {
	cfg := &config.Config{
		SMTP: config.SMTPConfig{
			Enabled:  true,
			Host:     "invalid.host.local",
			Port:     25,
			From:     "from@test.com",
			User:     "",
			Password: "",
		},
	}
	err := SendEmail(cfg, "to@test.com", "subj", "body")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "dial")
}

func TestSendEmail_Auth(t *testing.T) {
	server := newTestSMTPServer(t)
	defer server.Close()

	hostParts := strings.Split(server.addr, ":")
	host := hostParts[0]
	var port int
	_, err := fmt.Sscanf(hostParts[1], "%d", &port)
	require.NoError(t, err)

	cfg := &config.Config{
		SMTP: config.SMTPConfig{
			Enabled:  true,
			Host:     host,
			Port:     port,
			User:     "testuser",
			Password: "testpass",
			From:     "auth@test.com",
		},
	}

	err = SendEmail(cfg, "to@test.com", "subj", "body")
	require.NoError(t, err)

	server.WaitForMail(t, 2*time.Second)
	from, to, _, _ := server.LastMail()
	assert.Equal(t, "auth@test.com", from)
	assert.Equal(t, "to@test.com", to)
}

func TestSendEmail_AuthFailed(t *testing.T) {
	server := newTestSMTPServer(t)
	defer server.Close()
	server.failAuth = true

	hostParts := strings.Split(server.addr, ":")
	host := hostParts[0]
	var port int
	_, err := fmt.Sscanf(hostParts[1], "%d", &port)
	require.NoError(t, err)

	cfg := &config.Config{
		SMTP: config.SMTPConfig{
			Enabled:  true,
			Host:     host,
			Port:     port,
			User:     "testuser",
			Password: "wrong",
			From:     "auth@test.com",
		},
	}

	err = SendEmail(cfg, "to@test.com", "subj", "body")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "auth failed")
}

func TestSendEmail_MailFromFailed(t *testing.T) {
	server := newTestSMTPServer(t)
	defer server.Close()
	server.failMailFrom = true

	hostParts := strings.Split(server.addr, ":")
	host := hostParts[0]
	var port int
	_, err := fmt.Sscanf(hostParts[1], "%d", &port)
	require.NoError(t, err)

	cfg := &config.Config{
		SMTP: config.SMTPConfig{
			Enabled: true,
			Host:    host,
			Port:    port,
			From:    "from@test.com",
		},
	}

	err = SendEmail(cfg, "to@test.com", "subj", "body")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mail from failed")
}

func TestSendEmail_RcptToFailed(t *testing.T) {
	server := newTestSMTPServer(t)
	defer server.Close()
	server.failRcptTo = true

	hostParts := strings.Split(server.addr, ":")
	host := hostParts[0]
	var port int
	_, err := fmt.Sscanf(hostParts[1], "%d", &port)
	require.NoError(t, err)

	cfg := &config.Config{
		SMTP: config.SMTPConfig{
			Enabled: true,
			Host:    host,
			Port:    port,
			From:    "from@test.com",
		},
	}

	err = SendEmail(cfg, "to@test.com", "subj", "body")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "rcpt to failed")
}

func TestSendEmail_DataFailed(t *testing.T) {
	server := newTestSMTPServer(t)
	defer server.Close()
	server.failData = true

	hostParts := strings.Split(server.addr, ":")
	host := hostParts[0]
	var port int
	_, err := fmt.Sscanf(hostParts[1], "%d", &port)
	require.NoError(t, err)

	cfg := &config.Config{
		SMTP: config.SMTPConfig{
			Enabled: true,
			Host:    host,
			Port:    port,
			From:    "from@test.com",
		},
	}

	err = SendEmail(cfg, "to@test.com", "subj", "body")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "data failed")
}

func TestSendEmail_QuitFailed(t *testing.T) {
	server := newTestSMTPServer(t)
	defer server.Close()
	server.failQuit = true

	hostParts := strings.Split(server.addr, ":")
	host := hostParts[0]
	var port int
	_, err := fmt.Sscanf(hostParts[1], "%d", &port)
	require.NoError(t, err)

	cfg := &config.Config{
		SMTP: config.SMTPConfig{
			Enabled: true,
			Host:    host,
			Port:    port,
			From:    "from@test.com",
		},
	}

	err = SendEmail(cfg, "to@test.com", "subj", "body")
	require.NoError(t, err)
}

func TestEmailService_Send(t *testing.T) {
	server := newTestSMTPServer(t)
	defer server.Close()

	hostParts := strings.Split(server.addr, ":")
	host := hostParts[0]
	var port int
	_, err := fmt.Sscanf(hostParts[1], "%d", &port)
	require.NoError(t, err)

	cfg := &config.Config{
		SMTP: config.SMTPConfig{
			Enabled: true,
			Host:    host,
			Port:    port,
			From:    "service@test.com",
		},
	}

	svc := NewEmailService(cfg)
	err = svc.Send("to@test.com", "Service Subject", "Service Body")
	require.NoError(t, err)

	server.WaitForMail(t, 2*time.Second)
	_, _, body, headers := server.LastMail()
	assert.Contains(t, body, "Service Body")
	assert.Equal(t, "Service Subject", headers["Subject"])
}

// Бенчмарк для отправки письма (с тестовым сервером)
func BenchmarkSendEmail(b *testing.B) {
	server := newTestSMTPServer(&testing.T{})
	defer server.Close()

	hostParts := strings.Split(server.addr, ":")
	host := hostParts[0]
	var port int
	_, _ = fmt.Sscanf(hostParts[1], "%d", &port) // в бенчмарке игнорируем ошибку

	cfg := &config.Config{
		SMTP: config.SMTPConfig{
			Enabled: true,
			Host:    host,
			Port:    port,
			From:    "bench@test.com",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = SendEmail(cfg, "to@test.com", "bench subject", "bench body")
	}
}
