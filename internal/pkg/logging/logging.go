// internal/pkg/logging/logging.go
package logging

import (
	"context"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// CorrelationIDKeyType определяет тип ключа для correlation ID в контексте.
type CorrelationIDKeyType string

// CorrelationIDKey — ключ для хранения correlation ID в контексте.
const CorrelationIDKey CorrelationIDKeyType = "correlation_id"

// GetCorrelationID извлекает correlation ID из контекста.
// Если ID отсутствует, генерирует новый и сохраняет в контекст.
func GetCorrelationID(ctx context.Context) string {
	if id, ok := ctx.Value(CorrelationIDKey).(string); ok && id != "" {
		return id
	}
	// Генерируем новый ID
	id := uuid.New().String()
	return id
}

// SetCorrelationID устанавливает correlation ID в контекст.
func SetCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, CorrelationIDKey, id)
}

// Info — convenience функция для info логов.
func Info(ctx context.Context) *zerolog.Event {
	return log.Ctx(ctx).Info()
}

// Error — convenience функция для error логов.
func Error(ctx context.Context) *zerolog.Event {
	return log.Ctx(ctx).Error()
}

// Warn — convenience функция для warn логов.
func Warn(ctx context.Context) *zerolog.Event {
	return log.Ctx(ctx).Warn()
}

// Debug — convenience функция для debug логов.
func Debug(ctx context.Context) *zerolog.Event {
	return log.Ctx(ctx).Debug()
}
