// internal/pkg/email/queue.go
package email

import (
	"context"
	"fmt"
	"gengine-0/internal/config"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

const (
	defaultEmailWorkers   = 5
	defaultEmailInterval  = 10 * time.Second
	defaultEmailBatchSize = 10
)

var (
	globalService *EmailService
	serviceOnce   sync.Once
	workerCancel  context.CancelFunc
	workerCtx     context.Context
)

// InitQueue инициализирует глобальный EmailService и запускает воркер.
func InitQueue(cfg *config.Config, db *gorm.DB, workers int, interval time.Duration, batchSize int) {
	serviceOnce.Do(func() {
		if workers <= 0 {
			workers = defaultEmailWorkers
		}
		if interval <= 0 {
			interval = defaultEmailInterval
		}
		if batchSize <= 0 {
			batchSize = defaultEmailBatchSize
		}

		globalService = NewEmailService(cfg, db)

		// Сбрасываем retry-письма обратно в pending при старте,
		// чтобы они не потерялись после перезапуска сервера
		// TODO: use a status constant instead of raw strings
		if err := db.Model(&QueuedEmail{}).Where("status IN ?", []string{"retry", "sending"}).Update("status", "pending").Error; err != nil {
			log.Warn().Err(err).Msg("Failed to reset retry emails to pending")
		}

		workerCtx, workerCancel = context.WithCancel(context.Background())
		for i := 0; i < workers; i++ {
			go func() {
				globalService.StartWorker(workerCtx, interval, batchSize)
			}()
		}

		log.Info().Int("workers", workers).Dur("interval", interval).Msg("Email queue (persistent) initialized")
	})
}

// ShutdownQueue останавливает глобальный сервис и дожидается завершения воркеров.
func ShutdownQueue() {
	if workerCancel != nil {
		workerCancel()
	}
	if globalService != nil {
		globalService.Stop()
		globalService.wg.Wait()
	}
	log.Info().Msg("Email queue (persistent) stopped")
}

// Enqueue добавляет письмо в очередь (в БД).
func Enqueue(to, subject, body string) error {
	if globalService == nil {
		return fmt.Errorf("email service is not initialized")
	}
	return globalService.Send(to, subject, body)
}
