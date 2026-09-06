// Package log is a thin wrapper over zap, so that the rest of the tool logs
// through one configured logger without each package building its own.
package log

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// logger starts as a no-op rather than nil so that anything logging before
// cobra runs InitLogger degrades to silence instead of a nil dereference.
var logger = zap.NewNop()

// InitLogger configures the process logger. Debug selects zap's development
// encoder, which is readable at a terminal; otherwise output is JSON.
//
// Calling this is optional: until it runs, logging goes nowhere rather than
// panicking on a nil logger.
func InitLogger(debug bool) {
	var err error
	if debug {
		cfg := zap.NewDevelopmentConfig()
		cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
		cfg.DisableStacktrace = true
		logger, err = cfg.Build()
	} else {
		cfg := zap.NewProductionConfig()
		cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
		cfg.DisableStacktrace = true
		logger, err = cfg.Build()
	}
	if err != nil {
		panic(err)
	}
}

// Info logs at info level.
func Info(msg string, fields ...zap.Field) {
	logger.Info(msg, fields...)
}

// Error logs at error level.
func Error(msg string, fields ...zap.Field) {
	logger.Error(msg, fields...)
}

// Debug logs at debug level, which is silent unless InitLogger was
// given debug.
func Debug(msg string, fields ...zap.Field) {
	logger.Debug(msg, fields...)
}

// Warn logs at warning level.
func Warn(msg string, fields ...zap.Field) {
	logger.Warn(msg, fields...)
}

// Fatal logs at fatal level and then exits the process.
func Fatal(msg string, fields ...zap.Field) {
	logger.Fatal(msg, fields...)
}

// Sync flushes buffered log entries. Zap buffers writes, so without this the
// last few lines before exit can be lost.
func Sync() {
	if logger != nil {
		_ = logger.Sync()
	}
}
