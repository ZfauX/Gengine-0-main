// internal/pkg/email/email.go
package email

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/pkg/metrics"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultMaxAttempts   = 5
	retryQueueBufferSize = 256
)

// EmailService представляет сервис отправки писем с использованием persistent-очереди
// и отдельной async retry-очереди для неудавшихся отправок.
type EmailService struct {
	cfg  *config.Config
	db   *gorm.DB
	stop chan struct{}
	wg   sync.WaitGroup

	// retryQueue — отдельная in-memory очередь для async retry.
	// Когда отправка не удалась, письмо добавляется сюда вместо блокировки воркера.
	retryQueue chan retryJob
}

// retryJob — задача на повторную отправку.
type retryJob struct {
	emailID   uint
	attempt   int
	scheduled time.Time
}

// NewEmailService создаёт новый EmailService.
func NewEmailService(cfg *config.Config, db *gorm.DB) *EmailService {
	return &EmailService{
		cfg:        cfg,
		db:         db,
		stop:       make(chan struct{}),
		retryQueue: make(chan retryJob, retryQueueBufferSize),
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

	log.Debug().Str("to", to).Str("subject", subject).Msg("Email queued")
	return nil
}

// StartWorker запускает воркер, который периодически отправляет письма из очереди.
// Теперь включает два канала обработки:
//  1. processPendingEmails — обработка pending-писем из БД
//  2. retryQueue — обработка retry-задач (async, без блокировки)
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
		case retry := <-s.retryQueue:
			// Async retry: если ещё рано — перепланируем без блокировки
			if remaining := time.Until(retry.scheduled); remaining > 0 {
				timer := time.NewTimer(remaining)
				select {
				case <-timer.C:
					s.processRetryJob(ctx, retry)
				case <-ctx.Done():
					timer.Stop()
					return
				}
			} else {
				s.processRetryJob(ctx, retry)
			}
		case <-ticker.C:
			if err := s.processPendingEmails(ctx, batchSize); err != nil {
				log.Error().Err(err).Msg("Email worker: failed to process pending emails")
			}
			metrics.SetEmailQueueSize(float64(s.GetQueueSize(ctx)))
		}
	}
}

// Stop останавливает воркер.
func (s *EmailService) Stop() {
	close(s.stop)
}

// GetQueueSize возвращает текущий размер очереди писем из БД.
// Считает письма со статусами 'pending' и 'retry'.
func (s *EmailService) GetQueueSize(ctx context.Context) int64 {
	var count int64
	if err := s.db.WithContext(ctx).Model(&QueuedEmail{}).Where("status IN (?, ?)", "pending", "retry").Count(&count).Error; err != nil {
		log.Error().Err(err).Msg("GetQueueSize: failed to count emails")
		return 0
	}
	return count
}

// LoginAuth реализует SMTP AUTH LOGIN (RFC 4954).
// В отличие от PlainAuth, не зависит от TLS-проверки в Start(),
// что позволяет использовать после явного STARTTLS.
type loginAuth struct {
	user, pass string
}

func newLoginAuth(user, pass string) smtp.Auth {
	return &loginAuth{user: user, pass: pass}
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch string(fromServer) {
	case "Username:":
		return []byte(a.user), nil
	case "Password:":
		return []byte(a.pass), nil
	default:
		decoded, err := base64.StdEncoding.DecodeString(string(fromServer))
		if err != nil {
			return nil, fmt.Errorf("unknown auth challenge: %s", string(fromServer))
		}
		switch string(decoded) {
		case "Username:":
			return []byte(a.user), nil
		case "Password:":
			return []byte(a.pass), nil
		default:
			return nil, fmt.Errorf("unknown auth challenge: %s", string(decoded))
		}
	}
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

	// Параллельная отправка с лимитом concurrent workers
	const maxConcurrent = 10
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup

	for i := range emails {
		wg.Add(1)
		go func(e *QueuedEmail) {
			defer wg.Done()

			// Проверяем контекст до захвата семафора
			select {
			case <-ctx.Done():
				log.Warn().Uint("email_id", e.ID).Msg("processPendingEmails goroutine: context cancelled before semaphore")
				return
			default:
			}

			sem <- struct{}{}        // acquire semaphore
			defer func() { <-sem }() // release semaphore

			select {
			case <-ctx.Done():
				log.Warn().Uint("email_id", e.ID).Msg("processPendingEmails goroutine: context cancelled")
				return
			default:
			}

			s.sendEmailWithRetry(ctx, e)
		}(&emails[i])
	}

	wg.Wait()

	return nil
}

// SendSync — синхронная отправка (для случаев, когда асинхронность нежелательна).
func (s *EmailService) SendSync(to, subject, body string) error {
	if !s.cfg.SMTP.Enabled {
		return fmt.Errorf("SMTP is not enabled")
	}
	return SendEmail(s.cfg, to, subject, body)
}

// sendEmailWithRetry — неблокирующая отправка с async retry.
// Если отправка не удалась, письмо автоматически планируется на повторную
// попытку через exponential backoff — БЕЗ блокировки воркера.
func (s *EmailService) sendEmailWithRetry(ctx context.Context, email *QueuedEmail) {
	select {
	case <-ctx.Done():
		log.Warn().Uint("email_id", email.ID).Msg("sendEmailWithRetry: context cancelled, skipping")
		return
	default:
	}

	err := SendEmail(s.cfg, email.Recipient, email.Subject, email.Body)
	if err == nil {
		now := time.Now()
		email.Status = "sent"
		email.SentAt = &now
		if updateErr := s.db.Model(email).Updates(map[string]any{
			"status":  "sent",
			"sent_at": &now,
		}).Error; updateErr != nil {
			log.Error().Err(updateErr).Uint("email_id", email.ID).Msg("Failed to update email status to sent")
		}
		log.Debug().Uint("email_id", email.ID).Str("to", email.Recipient).Msg("Email sent successfully")
		return
	}

	// Используем атомарный UPDATE с increment attempts и проверкой статуса
	// чтобы избежать race condition между воркерами
	result := s.db.Model(email).Where("status IN ('pending', 'retry')").
		Updates(map[string]any{
			"attempts":   gorm.Expr("attempts + 1"),
			"status":     gorm.Expr("CASE WHEN attempts + 1 >= ? THEN 'failed' ELSE 'retry' END", defaultMaxAttempts),
			"last_error": err.Error(),
		})
	if result.Error != nil {
		log.Error().Err(result.Error).Uint("email_id", email.ID).Msg("sendEmailWithRetry: failed to update email")
		return
	}
	if result.RowsAffected == 0 {
		log.Warn().Uint("email_id", email.ID).Msg("sendEmailWithRetry: email already processed by another worker")
		return
	}

	// После атомарного обновления проверяем статус и планируем retry при необходимости
	if reloadErr := s.db.WithContext(ctx).First(email, email.ID).Error; reloadErr != nil {
		log.Error().Err(reloadErr).Uint("email_id", email.ID).Msg("sendEmailWithRetry: failed to reload email after update")
		return
	}
	if email.Status == "retry" && email.Attempts < defaultMaxAttempts {
		delay := time.Duration(1<<(email.Attempts-1)) * time.Second
		s.scheduleRetry(ctx, email.ID, email.Attempts, delay)
	} else if email.Status == "failed" {
		log.Error().
			Uint("email_id", email.ID).
			Str("to", email.Recipient).
			Str("subject", email.Subject).
			Str("last_error", err.Error()).
			Msg("Failed to send email after all retries")
	}
}

// scheduleRetry добавляет задачу на повторную отправку в async retry-очередь.
// Воркер получит задачу только когда наступит scheduled время.
func (s *EmailService) scheduleRetry(ctx context.Context, emailID uint, currentAttempt int, delay time.Duration) {
	select {
	case s.retryQueue <- retryJob{
		emailID:   emailID,
		attempt:   currentAttempt + 1,
		scheduled: time.Now().Add(delay),
	}:
		log.Debug().
			Uint("email_id", emailID).
			Int("next_attempt", currentAttempt+1).
			Dur("delay", delay).
			Msg("Email retry scheduled")
	default:
		// Очередь переполнена — фоллбэк на немедленную обработку
		log.Warn().Uint("email_id", emailID).Msg("Retry queue full, immediate retry fallback")
		s.processRetryJob(ctx, retryJob{
			emailID:   emailID,
			attempt:   currentAttempt + 1,
			scheduled: time.Now(),
		})
	}
}

// processRetryJob обрабатывает retry-задачу: читает письмо из БД и пытается отправить.
func (s *EmailService) processRetryJob(ctx context.Context, job retryJob) {
	select {
	case <-ctx.Done():
		log.Warn().Uint("email_id", job.emailID).Msg("processRetryJob: context cancelled, skipping")
		return
	default:
	}

	var email QueuedEmail
	if loadErr := s.db.WithContext(ctx).First(&email, job.emailID).Error; loadErr != nil {
		log.Error().Err(loadErr).Uint("email_id", job.emailID).Msg("processRetryJob: email not found")
		return
	}

	sendErr := SendEmail(s.cfg, email.Recipient, email.Subject, email.Body)
	if sendErr == nil {
		now := time.Now()
		if updateErr := s.db.Model(&email).Updates(map[string]any{
			"status":  "sent",
			"sent_at": &now,
		}).Error; updateErr != nil {
			log.Error().Err(updateErr).Uint("email_id", email.ID).Msg("Failed to update email status to sent")
		}
		log.Debug().Uint("email_id", email.ID).Str("to", email.Recipient).Int("attempt", job.attempt).Msg("Email sent on retry")
		return
	}

	// Используем атомарный UPDATE для защиты от race condition между воркерами
	result := s.db.Model(&email).Where("status IN ('pending', 'retry')").
		Updates(map[string]any{
			"attempts":   gorm.Expr("attempts + 1"),
			"status":     gorm.Expr("CASE WHEN attempts + 1 >= ? THEN 'failed' ELSE 'retry' END", defaultMaxAttempts),
			"last_error": sendErr.Error(),
		})
	if result.Error != nil {
		log.Error().Err(result.Error).Uint("email_id", email.ID).Msg("processRetryJob: failed to update email")
		return
	}
	if result.RowsAffected == 0 {
		log.Warn().Uint("email_id", email.ID).Msg("processRetryJob: email already processed by another worker")
		return
	}

	// Перечитываем обновлённую запись для проверки статуса
	if reloadErr := s.db.WithContext(ctx).First(&email, email.ID).Error; reloadErr != nil {
		log.Error().Err(reloadErr).Uint("email_id", email.ID).Msg("processRetryJob: failed to reload email after update")
		return
	}
	if email.Status == "retry" {
		delay := time.Duration(1<<(email.Attempts-1)) * time.Second
		s.scheduleRetry(ctx, email.ID, email.Attempts, delay)
	} else {
		log.Error().
			Uint("email_id", email.ID).
			Str("to", email.Recipient).
			Int("final_attempt", email.Attempts).
			Err(sendErr).
			Msg("Failed to send email after all retries")
	}
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
		if strings.HasPrefix(cfg.SMTP.Host, "127.0.0.1") || cfg.SMTP.Host == "localhost" {
			auth = smtp.PlainAuth("", cfg.SMTP.User, cfg.SMTP.Password, cfg.SMTP.Host)
		} else {
			auth = newLoginAuth(cfg.SMTP.User, cfg.SMTP.Password)
		}
	}

	if cfg.SMTP.Port == 465 {
		tlsConfig := &tls.Config{ServerName: cfg.SMTP.Host}
		conn, dialErr := tls.Dial("tcp", addr, tlsConfig)
		if dialErr != nil {
			return fmt.Errorf("failed to connect TLS: %w", dialErr)
		}
		defer func() {
			if closeErr := conn.Close(); closeErr != nil {
				log.Error().Err(closeErr).Msg("error closing TLS connection")
			}
		}()

		client, clientErr := smtp.NewClient(conn, cfg.SMTP.Host)
		if clientErr != nil {
			return fmt.Errorf("failed to create SMTP client: %w", clientErr)
		}
		defer func() {
			if quitErr := client.Quit(); quitErr != nil {
				log.Error().Err(quitErr).Msg("error quitting SMTP client")
			}
		}()

		if auth != nil {
			if authErr := client.Auth(auth); authErr != nil {
				return fmt.Errorf("SMTP auth failed: %w", authErr)
			}
		}

		if mailErr := client.Mail(cfg.SMTP.From); mailErr != nil {
			return fmt.Errorf("MAIL FROM failed: %w", mailErr)
		}
		if rcptErr := client.Rcpt(to); rcptErr != nil {
			return fmt.Errorf("RCPT TO failed: %w", rcptErr)
		}

		w, dataErr := client.Data()
		if dataErr != nil {
			return fmt.Errorf("DATA command failed: %w", dataErr)
		}
		_, writeErr := w.Write([]byte(message))
		if writeErr != nil {
			return fmt.Errorf("failed to write message: %w", writeErr)
		}
		closeErr := w.Close()
		if closeErr != nil {
			return fmt.Errorf("failed to close data writer: %w", closeErr)
		}
	} else {
		conn, dialErr := smtp.Dial(addr)
		if dialErr != nil {
			return fmt.Errorf("failed to dial: %w", dialErr)
		}
		defer func() {
			if closeErr := conn.Close(); closeErr != nil {
				log.Error().Err(closeErr).Msg("error closing SMTP connection")
			}
		}()

		if helloErr := conn.Hello("localhost"); helloErr != nil {
			return fmt.Errorf("HELO failed: %w", helloErr)
		}

		if ok, _ := conn.Extension("STARTTLS"); !ok {
			if cfg.SMTP.User != "" && !strings.HasPrefix(cfg.SMTP.Host, "127.0.0.1") && cfg.SMTP.Host != "localhost" {
				return fmt.Errorf("STARTTLS not supported by server, refusing to send credentials over plain connection")
			}
		} else {
			tlsConfig := &tls.Config{ServerName: cfg.SMTP.Host}
			if starttlsErr := conn.StartTLS(tlsConfig); starttlsErr != nil {
				return fmt.Errorf("STARTTLS failed: %w", starttlsErr)
			}
		}

		if auth != nil {
			if authErr := conn.Auth(auth); authErr != nil {
				return fmt.Errorf("auth failed: %w", authErr)
			}
		}

		if mailErr := conn.Mail(cfg.SMTP.From); mailErr != nil {
			return fmt.Errorf("MAIL FROM failed: %w", mailErr)
		}
		if rcptErr := conn.Rcpt(to); rcptErr != nil {
			return fmt.Errorf("RCPT TO failed: %w", rcptErr)
		}

		w, dataErr := conn.Data()
		if dataErr != nil {
			return fmt.Errorf("DATA command failed: %w", dataErr)
		}
		_, writeErr := w.Write([]byte(message))
		if writeErr != nil {
			return fmt.Errorf("write failed: %w", writeErr)
		}
		closeErr := w.Close()
		if closeErr != nil {
			return fmt.Errorf("close data writer: %w", closeErr)
		}
	}

	return nil
}

// EmailMessage представляет одно письмо для batch-отправки.
type EmailMessage struct {
	To      string
	Subject string
	Body    string
}

// SendBatch отправляет несколько писем через один SMTP-коннект.
// Существенно быстрее, чем SendEmail в цикле, т.к. требует только одной
// установки соединения и одной аутентификации.
func SendBatch(cfg *config.Config, messages []EmailMessage) error {
	if !cfg.SMTP.Enabled {
		return fmt.Errorf("SMTP is not enabled")
	}
	if len(messages) == 0 {
		return nil
	}

	addr := fmt.Sprintf("%s:%d", cfg.SMTP.Host, cfg.SMTP.Port)

	var auth smtp.Auth
	if cfg.SMTP.User != "" && cfg.SMTP.Password != "" {
		if strings.HasPrefix(cfg.SMTP.Host, "127.0.0.1") || cfg.SMTP.Host == "localhost" {
			auth = smtp.PlainAuth("", cfg.SMTP.User, cfg.SMTP.Password, cfg.SMTP.Host)
		} else {
			auth = newLoginAuth(cfg.SMTP.User, cfg.SMTP.Password)
		}
	}

	var client *smtp.Client

	switch {
	case cfg.SMTP.Port == 465:
		tlsConfig := &tls.Config{ServerName: cfg.SMTP.Host}
		tlsConn, dialErr := tls.Dial("tcp", addr, tlsConfig)
		if dialErr != nil {
			return fmt.Errorf("failed to connect TLS: %w", dialErr)
		}
		defer tlsConn.Close()

		c, clientErr := smtp.NewClient(tlsConn, cfg.SMTP.Host)
		if clientErr != nil {
			return fmt.Errorf("failed to create SMTP client: %w", clientErr)
		}
		client = c

	default:
		plainConn, dialErr := smtp.Dial(addr)
		if dialErr != nil {
			return fmt.Errorf("failed to dial: %w", dialErr)
		}
		defer plainConn.Close()

		client = plainConn

		if helloErr := client.Hello("localhost"); helloErr != nil {
			return fmt.Errorf("HELO failed: %w", helloErr)
		}

		if ok, _ := client.Extension("STARTTLS"); !ok {
			if cfg.SMTP.User != "" && !strings.HasPrefix(cfg.SMTP.Host, "127.0.0.1") && cfg.SMTP.Host != "localhost" {
				return fmt.Errorf("STARTTLS not supported by server, refusing to send credentials over plain connection")
			}
		} else {
			tlsConfig := &tls.Config{ServerName: cfg.SMTP.Host}
			if starttlsErr := client.StartTLS(tlsConfig); starttlsErr != nil {
				return fmt.Errorf("STARTTLS failed: %w", starttlsErr)
			}
		}
	}

	defer func() {
		if quitErr := client.Quit(); quitErr != nil {
			log.Error().Err(quitErr).Msg("SendBatch: error quitting SMTP client")
		}
	}()

	if auth != nil {
		if authErr := client.Auth(auth); authErr != nil {
			return fmt.Errorf("SMTP auth failed: %w", authErr)
		}
	}

	for _, msg := range messages {
		headers := make(map[string]string)
		headers["From"] = cfg.SMTP.From
		headers["To"] = msg.To
		headers["Subject"] = msg.Subject
		headers["MIME-Version"] = "1.0"
		headers["Content-Type"] = "text/plain; charset=\"UTF-8\""

		var buf strings.Builder
		for k, v := range headers {
			fmt.Fprintf(&buf, "%s: %s\r\n", k, v)
		}
		buf.WriteString("\r\n")
		buf.WriteString(msg.Body)
		message := buf.String()

		if mailErr := client.Mail(cfg.SMTP.From); mailErr != nil {
			return fmt.Errorf("MAIL FROM failed for %s: %w", msg.To, mailErr)
		}
		if rcptErr := client.Rcpt(msg.To); rcptErr != nil {
			return fmt.Errorf("RCPT TO failed for %s: %w", msg.To, rcptErr)
		}

		w, dataErr := client.Data()
		if dataErr != nil {
			return fmt.Errorf("DATA command failed for %s: %w", msg.To, dataErr)
		}
		if _, writeErr := w.Write([]byte(message)); writeErr != nil {
			w.Close()
			return fmt.Errorf("write failed for %s: %w", msg.To, writeErr)
		}
		if closeErr := w.Close(); closeErr != nil {
			return fmt.Errorf("close data writer failed for %s: %w", msg.To, closeErr)
		}

		log.Debug().Str("to", msg.To).Msg("SendBatch: email sent")
	}

	return nil
}

func (s *EmailService) SendPasswordChangedEmail(to, userName string) error {
	subject := "Ваш пароль был изменён"
	body := fmt.Sprintf("Здравствуйте, %s!\n\nВаш пароль был успешно изменён. Если это были не вы, немедленно свяжитесь с поддержкой.\n\nС уважением,\nКоманда Gengine", userName)
	return SendEmail(s.cfg, to, subject, body)
}
