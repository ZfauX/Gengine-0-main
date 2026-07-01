// internal/pkg/email/email.go
package email

import (
	"crypto/tls"
	"fmt"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/pkg/metrics"

	"github.com/rs/zerolog/log"
)

// EmailJob представляет задачу на отправку письма.
type EmailJob struct {
	To      string
	Subject string
	Body    string
}

// EmailQueue управляет асинхронной отправкой писем.
type EmailQueue struct {
	cfg      *config.Config
	queue    chan EmailJob
	workers  int
	wg       sync.WaitGroup
	stopChan chan struct{}
	once     sync.Once
	mu       sync.Mutex // для безопасного обновления метрики
}

var (
	globalQueue *EmailQueue
	queueOnce   sync.Once
)

// InitQueue инициализирует глобальную очередь отправки писем.
// workers — количество воркеров (горутин), одновременно отправляющих письма.
// queueSize — размер буфера канала.
func InitQueue(cfg *config.Config, workers, queueSize int) {
	queueOnce.Do(func() {
		if workers <= 0 {
			workers = 5
		}
		if queueSize <= 0 {
			queueSize = 100
		}
		globalQueue = &EmailQueue{
			cfg:      cfg,
			queue:    make(chan EmailJob, queueSize),
			workers:  workers,
			stopChan: make(chan struct{}),
		}
		globalQueue.start()
	})
}

// ShutdownQueue останавливает очередь и дожидается завершения всех отправляемых писем.
func ShutdownQueue() {
	if globalQueue != nil {
		globalQueue.shutdown()
	}
}

// start запускает воркеры.
func (q *EmailQueue) start() {
	for i := 0; i < q.workers; i++ {
		q.wg.Add(1)
		go q.worker(i)
	}
	log.Info().Int("workers", q.workers).Msg("Email queue started")
	// Обновляем метрику начального размера очереди
	q.updateMetric()
}

// worker — горутина, которая обрабатывает задания из очереди.
func (q *EmailQueue) worker(id int) {
	defer q.wg.Done()
	for {
		select {
		case job := <-q.queue:
			// Отправляем письмо с повторными попытками
			err := q.sendWithRetry(job, 3)
			if err != nil {
				log.Error().Err(err).
					Str("to", job.To).
					Str("subject", job.Subject).
					Int("worker", id).
					Msg("Failed to send email after retries")
			} else {
				log.Debug().
					Str("to", job.To).
					Str("subject", job.Subject).
					Int("worker", id).
					Msg("Email sent successfully")
			}
			// Обновляем метрику после обработки задания
			q.updateMetric()
		case <-q.stopChan:
			log.Debug().Int("worker", id).Msg("Email worker stopped")
			return
		}
	}
}

// sendWithRetry пытается отправить письмо с указанным количеством попыток.
func (q *EmailQueue) sendWithRetry(job EmailJob, retries int) error {
	var lastErr error
	for attempt := 1; attempt <= retries; attempt++ {
		err := SendEmail(q.cfg, job.To, job.Subject, job.Body)
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt < retries {
			delay := time.Duration(1<<(attempt-1)) * time.Second
			time.Sleep(delay)
		}
	}
	return fmt.Errorf("failed after %d attempts: %w", retries, lastErr)
}

// shutdown останавливает очередь и дожидается завершения всех воркеров.
func (q *EmailQueue) shutdown() {
	q.once.Do(func() {
		close(q.stopChan)
		q.wg.Wait()
		log.Info().Msg("Email queue stopped")
	})
}

// updateMetric обновляет Prometheus gauge с текущим размером очереди.
func (q *EmailQueue) updateMetric() {
	q.mu.Lock()
	defer q.mu.Unlock()
	size := float64(len(q.queue))
	metrics.SetEmailQueueSize(size)
}

// Enqueue добавляет задачу в очередь на отправку.
// Если очередь не инициализирована, возвращает ошибку.
// При переполнении очереди выполняет синхронную отправку как fallback.
// Возвращает ошибку, если SMTP отключён или отправка не удалась.
func Enqueue(to, subject, body string) error {
	if globalQueue == nil {
		return fmt.Errorf("email queue is not initialized")
	}
	if !globalQueue.cfg.SMTP.Enabled {
		return fmt.Errorf("SMTP is not enabled")
	}

	job := EmailJob{To: to, Subject: subject, Body: body}

	// Пытаемся добавить в очередь
	select {
	case globalQueue.queue <- job:
		// Обновляем метрику после добавления
		globalQueue.updateMetric()
		return nil
	default:
		// Очередь переполнена — выполняем синхронную отправку как fallback
		log.Warn().
			Str("to", to).
			Str("subject", subject).
			Msg("Email queue is full, sending synchronously as fallback")
		return SendEmail(globalQueue.cfg, to, subject, body)
	}
}

// EmailService представляет сервис отправки писем (синхронный или через очередь).
type EmailService struct {
	cfg *config.Config
}

// NewEmailService создаёт новый EmailService с настройками из конфигурации.
func NewEmailService(cfg *config.Config) *EmailService {
	return &EmailService{cfg: cfg}
}

// Send отправляет письмо. Если глобальная очередь инициализирована — использует её,
// иначе отправляет синхронно.
func (s *EmailService) Send(to, subject, body string) error {
	if globalQueue != nil {
		return Enqueue(to, subject, body)
	}
	// Если очередь не инициализирована, отправляем синхронно
	return SendEmail(s.cfg, to, subject, body)
}

// SendSync — синхронная отправка (для случаев, когда асинхронность нежелательна).
func (s *EmailService) SendSync(to, subject, body string) error {
	return SendEmail(s.cfg, to, subject, body)
}

// SendEmail – низкоуровневая функция отправки письма (может использоваться напрямую).
// Она не использует очередь, а отправляет сразу.
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
