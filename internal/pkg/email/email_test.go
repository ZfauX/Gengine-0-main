// internal/pkg/email/email_test.go
package email

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// =============================================================================
// Вспомогательный тестовый SMTP-сервер (без изменений)
// =============================================================================

type testSMTPServer struct {
	addr     string
	listener net.Listener
	lastMail struct {
		from    string
		to      string
		data    string
		headers map[string]string
		body    string
	}
	mu           sync.Mutex
	mailReceived chan struct{}
	failAuth     bool
	failMailFrom bool
	failRcptTo   bool
	failData     bool
	failQuit     bool
	started      bool
	shutdown     chan struct{}
	wg           sync.WaitGroup
}

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
	s.wg.Add(1)
	go s.serve()
	// Ждём запуска сервера
	assert.Eventually(t, func() bool {
		conn, err := net.Dial("tcp", s.addr)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, 2*time.Second, 50*time.Millisecond)
	return s
}

func (s *testSMTPServer) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.shutdown:
				return
			default:
				continue
			}
		}
		go s.handleConn(conn)
	}
}

func (s *testSMTPServer) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	if err := s.writeResponse(writer, 220, "localhost test SMTP"); err != nil {
		return
	}
	_ = writer.Flush()

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
			_ = s.writeResponse(writer, 250, "localhost")
			_ = writer.Flush()
		case "MAIL":
			if s.failMailFrom {
				_ = s.writeResponse(writer, 550, "mail from failed")
				_ = writer.Flush()
				continue
			}
			from = strings.TrimPrefix(arg, "FROM:")
			from = strings.Trim(from, "<>")
			_ = s.writeResponse(writer, 250, "OK")
			_ = writer.Flush()
		case "RCPT":
			if s.failRcptTo {
				_ = s.writeResponse(writer, 550, "rcpt to failed")
				_ = writer.Flush()
				continue
			}
			to = strings.TrimPrefix(arg, "TO:")
			to = strings.Trim(to, "<>")
			_ = s.writeResponse(writer, 250, "OK")
			_ = writer.Flush()
		case "DATA":
			if s.failData {
				_ = s.writeResponse(writer, 550, "data failed")
				_ = writer.Flush()
				continue
			}
			_ = s.writeResponse(writer, 354, "Start mail input; end with <CRLF>.<CRLF>")
			_ = writer.Flush()
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

			_ = s.writeResponse(writer, 250, "OK")
			_ = writer.Flush()
		case "QUIT":
			if s.failQuit {
				_ = s.writeResponse(writer, 500, "quit failed")
				_ = writer.Flush()
				continue
			}
			_ = s.writeResponse(writer, 221, "Bye")
			_ = writer.Flush()
			return
		case "AUTH":
			if s.failAuth {
				_ = s.writeResponse(writer, 535, "auth failed")
				_ = writer.Flush()
				continue
			}
			_ = s.writeResponse(writer, 235, "Authentication successful")
			_ = writer.Flush()
		default:
			_ = s.writeResponse(writer, 502, "Command not implemented")
			_ = writer.Flush()
		}
	}
}

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
	s.wg.Wait()
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
// Вспомогательная функция для создания тестовой БД (PostgreSQL через testutil)
// =============================================================================

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := testutil.SetupPostgresDBOrSkip(t, &QueuedEmail{})
	return db
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
	db := setupTestDB(t)
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

	svc := NewEmailService(cfg, db)
	err = svc.Send("to@test.com", "Service Subject", "Service Body")
	require.NoError(t, err)

	// Проверяем, что письмо сохранено в БД
	var queued QueuedEmail
	err = db.Where("recipient = ?", "to@test.com").First(&queued).Error
	require.NoError(t, err)
	assert.Equal(t, "pending", queued.Status)
	assert.Equal(t, "Service Subject", queued.Subject)
	assert.Equal(t, "Service Body", queued.Body)

	// Теперь запускаем воркер для отправки
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	svc.StartWorker(ctx, 100*time.Millisecond, 10)

	// Ждём, пока письмо отправится
	server.WaitForMail(t, 3*time.Second)

	// Проверяем, что статус обновился
	err = db.First(&queued, queued.ID).Error
	require.NoError(t, err)
	assert.Equal(t, "sent", queued.Status)
	assert.NotNil(t, queued.SentAt)

	_, _, body, headers := server.LastMail()
	assert.Contains(t, body, "Service Body")
	assert.Equal(t, "Service Subject", headers["Subject"])
}

func BenchmarkSendEmail(b *testing.B) {
	server := &testSMTPServer{
		mailReceived: make(chan struct{}, 1),
		shutdown:     make(chan struct{}),
	}
	var err error
	server.listener, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	server.addr = server.listener.Addr().String()
	server.started = true
	server.wg.Add(1)
	go server.serve()
	// Ждём запуска сервера
	assert.Eventually(b, func() bool {
		conn, e := net.Dial("tcp", server.addr)
		if e != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, 2*time.Second, 50*time.Millisecond)
	defer server.Close()

	hostParts := strings.Split(server.addr, ":")
	host := hostParts[0]
	var port int
	_, _ = fmt.Sscanf(hostParts[1], "%d", &port)

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
