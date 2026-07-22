package logging

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/logger"
)

func TestGetCorrelationID_New(t *testing.T) {
	ctx := context.Background()
	id := GetCorrelationID(ctx)
	assert.NotEmpty(t, id)
	// Should be a valid UUID
	assert.Len(t, id, 36)
}

func TestGetCorrelationID_Existing(t *testing.T) {
	ctx := SetCorrelationID(context.Background(), "test-id-123")
	id := GetCorrelationID(ctx)
	assert.Equal(t, "test-id-123", id)
}

func TestGetCorrelationID_Empty(t *testing.T) {
	ctx := SetCorrelationID(context.Background(), "")
	id := GetCorrelationID(ctx)
	assert.NotEmpty(t, id)
	assert.NotEqual(t, "", id)
}

func TestSetCorrelationID(t *testing.T) {
	ctx := SetCorrelationID(context.Background(), "my-id")
	val := ctx.Value(CorrelationIDKey)
	assert.Equal(t, "my-id", val)
}

func TestCorrelationIDKeyType(t *testing.T) {
	ctx := context.WithValue(context.Background(), CorrelationIDKey, "key-value")
	assert.Equal(t, "key-value", GetCorrelationID(ctx))
	// Different key type should not match - use a different type to avoid collision
	type otherKeyType string
	ctx2 := context.WithValue(context.Background(), otherKeyType("correlation_id"), "wrong-key")
	assert.NotEqual(t, "wrong-key", GetCorrelationID(ctx2))
}

func TestConvenienceLogging(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf).Level(zerolog.DebugLevel)
	ctx := logger.WithContext(context.Background())

	Info(ctx).Msg("info msg")
	assert.True(t, strings.Contains(buf.String(), `"message":"info msg"`))
	buf.Reset()

	Error(ctx).Err(assert.AnError).Msg("error msg")
	assert.True(t, strings.Contains(buf.String(), `"message":"error msg"`))
	buf.Reset()

	Warn(ctx).Msg("warn msg")
	assert.True(t, strings.Contains(buf.String(), `"message":"warn msg"`))
	buf.Reset()

	Debug(ctx).Msg("debug msg")
	assert.True(t, strings.Contains(buf.String(), `"message":"debug msg"`))
}

func TestConvenienceLogging_NoContextLogger(t *testing.T) {
	// When context has no logger, should use global logger (doesn't panic)
	ctx := context.Background()
	assert.NotPanics(t, func() {
		Info(ctx).Msg("test")
	})
}

func TestNewSentryWriter(t *testing.T) {
	w := NewSentryWriter(time.Second)
	require.NotNil(t, w)
	assert.Len(t, w.levels, 3)
	assert.Contains(t, w.levels, zerolog.ErrorLevel)
	assert.Contains(t, w.levels, zerolog.FatalLevel)
	assert.Contains(t, w.levels, zerolog.PanicLevel)
}

func TestSentryWriter_Write(t *testing.T) {
	w := NewSentryWriter(time.Second)
	n, err := w.Write([]byte("test data"))
	assert.NoError(t, err)
	assert.Equal(t, len("test data"), n)
}

func TestSentryWriter_WriteLevel_Filtered(t *testing.T) {
	w := NewSentryWriter(time.Second)
	n, err := w.WriteLevel(zerolog.DebugLevel, []byte("debug msg"))
	assert.NoError(t, err)
	assert.Equal(t, len("debug msg"), n)
}

func TestSentryWriter_Flush(t *testing.T) {
	w := NewSentryWriter(time.Millisecond)
	// Should not panic
	assert.NotPanics(t, func() {
		w.Flush()
	})
}

func TestSentryWriter_WithNilFlush(t *testing.T) {
	w := &SentryWriter{levels: []zerolog.Level{zerolog.ErrorLevel}}
	assert.NotPanics(t, func() {
		w.Flush()
	})
}

func TestSentryWriter_CustomLevels(t *testing.T) {
	w := &SentryWriter{
		levels: []zerolog.Level{zerolog.WarnLevel},
	}
	n, err := w.WriteLevel(zerolog.WarnLevel, []byte("custom"))
	assert.NoError(t, err)
	assert.Equal(t, len("custom"), n)

	// Error level not in custom levels, so not captured
	n, err = w.WriteLevel(zerolog.ErrorLevel, []byte("not captured"))
	assert.NoError(t, err)
	assert.Equal(t, len("not captured"), n)
}

func TestSentryError_Error(t *testing.T) {
	e := &sentryError{msg: "test error"}
	assert.Equal(t, "test error", e.Error())
}

func TestGormLogger_LogMode(t *testing.T) {
	l := &GormLogger{LogLevel: logger.Info}
	infoLogger := l.LogMode(logger.Info)
	assert.Equal(t, logger.Info, infoLogger.(*GormLogger).LogLevel)

	warnLogger := l.LogMode(logger.Warn)
	assert.Equal(t, logger.Warn, warnLogger.(*GormLogger).LogLevel)
}

func TestGormLogger_Info(t *testing.T) {
	var buf bytes.Buffer
	log.Logger = zerolog.New(&buf).Level(zerolog.DebugLevel)
	defer func() { log.Logger = zerolog.New(zerolog.ConsoleWriter{}) }()

	l := &GormLogger{LogLevel: logger.Info}
	l.Info(context.Background(), "test %s", "info")
	assert.True(t, strings.Contains(buf.String(), "test info"))
}

func TestGormLogger_Info_BelowLevel(t *testing.T) {
	var buf bytes.Buffer
	log.Logger = zerolog.New(&buf).Level(zerolog.DebugLevel)
	defer func() { log.Logger = zerolog.New(zerolog.ConsoleWriter{}) }()

	l := &GormLogger{LogLevel: logger.Warn}
	l.Info(context.Background(), "should not appear")
	assert.Empty(t, buf.String())
}

func TestGormLogger_Warn(t *testing.T) {
	var buf bytes.Buffer
	log.Logger = zerolog.New(&buf).Level(zerolog.DebugLevel)
	defer func() { log.Logger = zerolog.New(zerolog.ConsoleWriter{}) }()

	l := &GormLogger{LogLevel: logger.Warn}
	l.Warn(context.Background(), "warn %d", 42)
	assert.True(t, strings.Contains(buf.String(), "warn 42"))
}

func TestGormLogger_Warn_BelowLevel(t *testing.T) {
	var buf bytes.Buffer
	log.Logger = zerolog.New(&buf).Level(zerolog.DebugLevel)
	defer func() { log.Logger = zerolog.New(zerolog.ConsoleWriter{}) }()

	l := &GormLogger{LogLevel: logger.Error}
	l.Warn(context.Background(), "should not appear")
	assert.Empty(t, buf.String())
}

func TestGormLogger_Error(t *testing.T) {
	var buf bytes.Buffer
	log.Logger = zerolog.New(&buf).Level(zerolog.DebugLevel)
	defer func() { log.Logger = zerolog.New(zerolog.ConsoleWriter{}) }()

	l := &GormLogger{LogLevel: logger.Error}
	l.Error(context.Background(), "error %s", "err")
	assert.True(t, strings.Contains(buf.String(), "error err"))
}

func TestGormLogger_Error_BelowLevel(t *testing.T) {
	var buf bytes.Buffer
	log.Logger = zerolog.New(&buf).Level(zerolog.DebugLevel)
	defer func() { log.Logger = zerolog.New(zerolog.ConsoleWriter{}) }()

	l := &GormLogger{LogLevel: logger.Silent}
	l.Error(context.Background(), "should not appear")
	assert.Empty(t, buf.String())
}

func TestGormLogger_Trace(t *testing.T) {
	var buf bytes.Buffer
	log.Logger = zerolog.New(&buf).Level(zerolog.DebugLevel)
	defer func() { log.Logger = zerolog.New(zerolog.ConsoleWriter{}) }()

	l := &GormLogger{LogLevel: logger.Info}
	begin := time.Now()
	l.Trace(context.Background(), begin, func() (string, int64) {
		return "SELECT 1", 1
	}, nil)
	assert.True(t, strings.Contains(buf.String(), "SELECT 1"))
	assert.True(t, strings.Contains(buf.String(), "GORM trace"))
}

func TestGormLogger_Trace_Silent(t *testing.T) {
	var buf bytes.Buffer
	log.Logger = zerolog.New(&buf).Level(zerolog.DebugLevel)
	defer func() { log.Logger = zerolog.New(zerolog.ConsoleWriter{}) }()

	l := &GormLogger{LogLevel: logger.Silent}
	l.Trace(context.Background(), time.Now(), func() (string, int64) {
		return "SELECT 1", 1
	}, nil)
	assert.Empty(t, buf.String())
}

func TestGormLogger_Trace_WithError(t *testing.T) {
	var buf bytes.Buffer
	log.Logger = zerolog.New(&buf).Level(zerolog.DebugLevel)
	defer func() { log.Logger = zerolog.New(zerolog.ConsoleWriter{}) }()

	l := &GormLogger{LogLevel: logger.Info}
	l.Trace(context.Background(), time.Now(), func() (string, int64) {
		return "SELECT 1", 0
	}, assert.AnError)
	assert.True(t, strings.Contains(buf.String(), "error"))
}

func TestGormLogger_LogMode_ReturnsCopy(t *testing.T) {
	l := &GormLogger{LogLevel: logger.Info}
	l2 := l.LogMode(logger.Warn)
	// Original should be unchanged
	assert.Equal(t, logger.Info, l.LogLevel)
	// New should have the new level
	assert.Equal(t, logger.Warn, l2.(*GormLogger).LogLevel)
}
