// internal/pkg/email/email.go
package email

import (
	"crypto/tls"
	"fmt"
	"net/smtp"
	"strings"

	"gengine-0/internal/config"

	"github.com/rs/zerolog/log"
)

// EmailService представляет сервис отправки писем.
type EmailService struct {
	cfg *config.Config
}

// NewEmailService создаёт новый EmailService с настройками из конфигурации.
func NewEmailService(cfg *config.Config) *EmailService {
	return &EmailService{cfg: cfg}
}

// Send отправляет письмо, используя настройки SMTP.
func (s *EmailService) Send(to, subject, body string) error {
	return SendEmail(s.cfg, to, subject, body)
}

// SendEmail – низкоуровневая функция отправки письма (может использоваться напрямую).
func SendEmail(cfg *config.Config, to, subject, body string) error {
	if !cfg.SMTP.Enabled {
		return fmt.Errorf("SMTP is not enabled")
	}

	addr := fmt.Sprintf("%s:%d", cfg.SMTP.Host, cfg.SMTP.Port)

	headers := make(map[string]string)
	headers["From"] = cfg.SMTP.From
	headers["To"] = to
	headers["Subject"] = subject
	headers["MIME-Version"] = "1.0"
	headers["Content-Type"] = "text/plain; charset=\"UTF-8\""

	var msg strings.Builder
	for k, v := range headers {
		fmt.Fprintf(&msg, "%s: %s\r\n", k, v)
	}
	msg.WriteString("\r\n")
	msg.WriteString(body)
	message := msg.String()

	var auth smtp.Auth
	if cfg.SMTP.User != "" && cfg.SMTP.Password != "" {
		auth = smtp.PlainAuth("", cfg.SMTP.User, cfg.SMTP.Password, cfg.SMTP.Host)
	}

	if cfg.SMTP.Port == 465 {
		tlsConfig := &tls.Config{ServerName: cfg.SMTP.Host}
		conn, err := tls.Dial("tcp", addr, tlsConfig)
		if err != nil {
			return fmt.Errorf("failed to connect TLS: %w", err)
		}
		defer func() {
			if err := conn.Close(); err != nil {
				log.Error().Err(err).Msg("error closing TLS connection")
			}
		}()

		client, err := smtp.NewClient(conn, cfg.SMTP.Host)
		if err != nil {
			return fmt.Errorf("failed to create SMTP client: %w", err)
		}
		defer func() {
			if err := client.Quit(); err != nil {
				log.Error().Err(err).Msg("error quitting SMTP client")
			}
		}()

		if auth != nil {
			if err = client.Auth(auth); err != nil {
				return fmt.Errorf("SMTP auth failed: %w", err)
			}
		}

		if err = client.Mail(cfg.SMTP.From); err != nil {
			return fmt.Errorf("MAIL FROM failed: %w", err)
		}
		if err = client.Rcpt(to); err != nil {
			return fmt.Errorf("RCPT TO failed: %w", err)
		}

		w, err := client.Data()
		if err != nil {
			return fmt.Errorf("DATA command failed: %w", err)
		}
		_, err = w.Write([]byte(message))
		if err != nil {
			return fmt.Errorf("failed to write message: %w", err)
		}
		err = w.Close()
		if err != nil {
			return fmt.Errorf("failed to close data writer: %w", err)
		}
	} else {
		conn, err := smtp.Dial(addr)
		if err != nil {
			return fmt.Errorf("failed to dial: %w", err)
		}
		defer func() {
			if err := conn.Close(); err != nil {
				log.Error().Err(err).Msg("error closing SMTP connection")
			}
		}()

		if err = conn.Hello("localhost"); err != nil {
			return fmt.Errorf("HELO failed: %w", err)
		}

		if ok, _ := conn.Extension("STARTTLS"); ok {
			tlsConfig := &tls.Config{ServerName: cfg.SMTP.Host}
			if err = conn.StartTLS(tlsConfig); err != nil {
				return fmt.Errorf("STARTTLS failed: %w", err)
			}
		}

		if auth != nil {
			if err = conn.Auth(auth); err != nil {
				return fmt.Errorf("auth failed: %w", err)
			}
		}

		if err = conn.Mail(cfg.SMTP.From); err != nil {
			return fmt.Errorf("MAIL FROM failed: %w", err)
		}
		if err = conn.Rcpt(to); err != nil {
			return fmt.Errorf("RCPT TO failed: %w", err)
		}

		w, err := conn.Data()
		if err != nil {
			return fmt.Errorf("DATA command failed: %w", err)
		}
		_, err = w.Write([]byte(message))
		if err != nil {
			return fmt.Errorf("write failed: %w", err)
		}
		err = w.Close()
		if err != nil {
			return fmt.Errorf("close data writer: %w", err)
		}
	}

	return nil
}