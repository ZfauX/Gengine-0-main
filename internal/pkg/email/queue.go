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
			workers = 5
		}
		if interval <= 0 {
			interval = 10 * time.Second
		}
		if batchSize <= 0 {
			batchSize = 10
		}

		globalService = NewEmailService(cfg, db)

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
