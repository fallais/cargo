package log

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// logger starts as a no-op rather than nil so that anything logging before
// cobra runs InitLogger degrades to silence instead of a nil dereference.
var logger = zap.NewNop()

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

func Info(msg string, fields ...zap.Field) {
	logger.Info(msg, fields...)
}

func Error(msg string, fields ...zap.Field) {
	logger.Error(msg, fields...)
}

func Debug(msg string, fields ...zap.Field) {
	logger.Debug(msg, fields...)
}

func Warn(msg string, fields ...zap.Field) {
	logger.Warn(msg, fields...)
}

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
