// internal/pkg/email/email.go
package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/smtp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/pkg/metrics"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EmailService представляет сервис отправки писем с использованием persistent-очереди.
type EmailService struct {
	cfg  *config.Config
	db   *gorm.DB
	stop chan struct{}
	wg   sync.WaitGroup

	queueSize atomic.Int64 // атомарный счётчик для предотвращения race condition
}

// NewEmailService создаёт новый EmailService.
func NewEmailService(cfg *config.Config, db *gorm.DB) *EmailService {
	return &EmailService{
		cfg:  cfg,
		db:   db,
		stop: make(chan struct{}),
	}
}

// Send отправляет письмо асинхронно — сохраняет в БД для последующей отправки воркером.
func (s *EmailService) Send(to, subject, body string) error {
	if !s.cfg.SMTP.Enabled {
		return fmt.Errorf("SMTP is not enabled")
	}

	email := &QueuedEmail{
		Recipient: to,
		Subject:   subject,
		Body:      body,
		Status:    "pending",
	}

	if err := s.db.Create(email).Error; err != nil {
		return fmt.Errorf("failed to queue email: %w", err)
	}

	s.queueSize.Add(1)
	metrics.SetEmailQueueSize(float64(s.queueSize.Load()))
	return nil
}

// SendSync — синхронная отправка (для случаев, когда асинхронность нежелательна).
func (s *EmailService) SendSync(to, subject, body string) error {
	if !s.cfg.SMTP.Enabled {
		return fmt.Errorf("SMTP is not enabled")
	}
	return SendEmail(s.cfg, to, subject, body)
}

// StartWorker запускает воркер, который периодически отправляет письма из очереди.
func (s *EmailService) StartWorker(ctx context.Context, interval time.Duration, batchSize int) {
	s.wg.Add(1)
	defer s.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("Email worker: context cancelled, stopping")
			return
		case <-s.stop:
			log.Info().Msg("Email worker: stopped")
			return
		case <-ticker.C:
			if err := s.processPendingEmails(ctx, batchSize); err != nil {
				log.Error().Err(err).Msg("Email worker: failed to process pending emails")
			}
			metrics.SetEmailQueueSize(float64(s.queueSize.Load()))
		}
	}
}

// Stop останавливает воркер.
func (s *EmailService) Stop() {
	close(s.stop)
}

// processPendingEmails обрабатывает письма со статусом 'pending'.
func (s *EmailService) processPendingEmails(ctx context.Context, batchSize int) error {
	var emails []QueuedEmail

	if err := s.db.WithContext(ctx).
		Where("status = ? AND (scheduled_at IS NULL OR scheduled_at <= ?)", "pending", time.Now()).
		Order("created_at ASC").
		Limit(batchSize).
		Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
		Find(&emails).Error; err != nil {
		return err
	}

	if len(emails) == 0 {
		return nil
	}

	// O6: Parallel sending с лимитом concurrent workers
	const maxConcurrent = 10 // максимум 10 параллельных SMTP-соединений
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup

	for i := range emails {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		wg.Add(1)
		go func(e *QueuedEmail) {
			defer wg.Done()
			sem <- struct{}{}        // acquire semaphore
			defer func() { <-sem }() // release semaphore

			s.sendEmailWithRetry(e)
		}(&emails[i])
	}

	wg.Wait()

	// queueSize уже уменьшен по факту отправки/ошибки каждого письма в sendEmailWithRetry
	return nil
}

// sendEmailWithRetry пытается отправить письмо и обновляет статус в БД.
// Уменьшает queueSize по факту отправки/ошибки каждого письма (не батчем).
func (s *EmailService) sendEmailWithRetry(email *QueuedEmail) {
	const maxAttempts = 3

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := SendEmail(s.cfg, email.Recipient, email.Subject, email.Body)
		if err == nil {
			now := time.Now()
			email.Status = "sent"
			email.SentAt = &now
			if err := s.db.Save(email).Error; err != nil {
				log.Error().Err(err).Uint("email_id", email.ID).Msg("Failed to update email status to sent")
			}
			log.Debug().Uint("email_id", email.ID).Str("to", email.Recipient).Msg("Email sent successfully")
			// Уменьшаем счётчик по факту отправки
			s.queueSize.Add(-1)
			return
		}
		lastErr = err
		email.Attempts = attempt
		email.LastError = err.Error()

		if attempt < maxAttempts {
			delay := time.Duration(1<<(attempt-1)) * time.Second
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-s.stop:
				timer.Stop()
				log.Info().Uint("email_id", email.ID).Msg("sendEmailWithRetry: stopped during backoff")
				s.queueSize.Add(-1)
				return
			}
			timer.Stop()
		}
	}

	// Все попытки исчерпаны
	email.Status = "failed"
	if err := s.db.Save(email).Error; err != nil {
		log.Error().Err(err).Uint("email_id", email.ID).Msg("Failed to update email status to failed")
	}

	// Уменьшаем счётчик по факту ошибки
	s.queueSize.Add(-1)

	log.Error().
		Uint("email_id", email.ID).
		Str("to", email.Recipient).
		Str("subject", email.Subject).
		Err(lastErr).
		Msg("Failed to send email after all retries")
}

// SendEmail – экспортируемая функция для синхронной отправки письма (используется в тестах и для обратной совместимости).
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
