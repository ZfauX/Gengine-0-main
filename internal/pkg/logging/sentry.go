package logging

import (
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/rs/zerolog"
)

type SentryWriter struct {
	levels []zerolog.Level
	flush  func()
}

func NewSentryWriter(flushTimeout time.Duration) *SentryWriter {
	return &SentryWriter{
		levels: []zerolog.Level{zerolog.ErrorLevel, zerolog.FatalLevel, zerolog.PanicLevel},
		flush: func() {
			sentry.Flush(flushTimeout)
		},
	}
}

func (w *SentryWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func (w *SentryWriter) WriteLevel(level zerolog.Level, p []byte) (int, error) {
	for _, l := range w.levels {
		if level == l {
			sentry.CaptureException(&sentryError{msg: string(p)})
			return len(p), nil
		}
	}
	return len(p), nil
}

func (w *SentryWriter) Flush() {
	if w.flush != nil {
		w.flush()
	}
}

type sentryError struct {
	msg string
}

func (e *sentryError) Error() string {
	return e.msg
}
