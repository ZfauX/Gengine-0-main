// Package logging предоставляет адаптеры логгирования для внешних библиотек.
package logging

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm/logger"
)

// GormLogger адаптирует zerolog для GORM v2.
type GormLogger struct {
	LogLevel logger.LogLevel
}

// LogMode возвращает новый логгер с указанным уровнем.
func (l *GormLogger) LogMode(level logger.LogLevel) logger.Interface {
	newLogger := *l
	newLogger.LogLevel = level
	return &newLogger
}

// Info логирует информационное сообщение.
func (l *GormLogger) Info(_ context.Context, msg string, data ...any) {
	if l.LogLevel >= logger.Info {
		log.Info().Msgf(msg, data...)
	}
}

// Warn логирует предупреждение.
func (l *GormLogger) Warn(_ context.Context, msg string, data ...any) {
	if l.LogLevel >= logger.Warn {
		log.Warn().Msgf(msg, data...)
	}
}

// Error логирует ошибку.
func (l *GormLogger) Error(_ context.Context, msg string, data ...any) {
	if l.LogLevel >= logger.Error {
		log.Error().Msgf(msg, data...)
	}
}

// Trace логирует SQL-запросы GORM.
func (l *GormLogger) Trace(_ context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	if l.LogLevel <= logger.Silent {
		return
	}
	elapsed := time.Since(begin)
	sql, rows := fc()
	log.Debug().
		Dur("elapsed", elapsed).
		Int64("rows", rows).
		Str("sql", sql).
		Err(err).
		Msg("GORM trace")
}
