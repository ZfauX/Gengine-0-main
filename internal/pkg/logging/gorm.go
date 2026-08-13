// Package logging предоставляет адаптеры логгирования для внешних библиотек.
package logging

import (
	"context"
	"regexp"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm/logger"
)

// sqlRedactRe (perf F1, PASS-10): прекомпилированный regexp — раньше
// regexp.MustCompile выполнялся на КАЖДЫЙ SQL-запрос (компиляция + аллокации).
var sqlRedactRe = regexp.MustCompile(`'[^']*@[^']*'`)

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

// redactSensitive заменяет потенциально конфиденциальные данные в SQL-запросах
func redactSensitive(sql string) string {
	// perf F1 (PASS-10): прекомпилированный regexp вместо MustCompile на каждый вызов.
	return sqlRedactRe.ReplaceAllString(sql, "'***@***'")
}

// Trace логирует SQL-запросы GORM.
func (l *GormLogger) Trace(_ context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	if l.LogLevel <= logger.Silent {
		return
	}
	elapsed := time.Since(begin)
	// perf F1 (PASS-10): fc() вызывается ТОЛЬКО когда Debug-лог реально включён —
	// раньше GORM форматировал полный SQL на каждый запрос даже при LogLevel=Warn.
	// Reviewer H1 (PASS-10): используем zerolog.GlobalLevel() — GetLevel() инстанса
	// всегда TraceLevel, т.к. main.go не вызывает Logger.Level().
	if zerolog.GlobalLevel() > zerolog.DebugLevel {
		// Форматирование SQL дорогое; для Warn/Error достаточно метаданных.
		// SQL-ОШИБКИ логируются на уровне Warn, чтобы не терять наблюдаемость
		// в production (раньше при Info/Warn err молча пропадал).
		if err != nil {
			log.Warn().
				Dur("elapsed", elapsed).
				Err(err).
				Msg("GORM trace (SQL error)")
			return
		}
		log.Debug().
			Dur("elapsed", elapsed).
			Err(err).
			Msg("GORM trace")
		return
	}
	sql, rows := fc()
	log.Debug().
		Dur("elapsed", elapsed).
		Int64("rows", rows).
		Str("sql", redactSensitive(sql)).
		Err(err).
		Msg("GORM trace")
}
