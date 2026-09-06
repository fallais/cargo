// Package log is a thin wrapper over zap, so that the rest of the tool logs
// through one configured logger without each package building its own.
package log

import (
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// logger starts as a no-op rather than nil so that anything logging before
// cobra runs InitLogger degrades to silence instead of a nil dereference.
var logger = zap.NewNop()

// config builds the shared logger configuration. Debug selects zap's
// development encoder, which is readable at a terminal; otherwise output is
// JSON.
func config(debug bool) zap.Config {
	cfg := zap.NewProductionConfig()
	if debug {
		cfg = zap.NewDevelopmentConfig()
	}
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	cfg.DisableStacktrace = true
	return cfg
}

// InitLogger configures logging to stderr.
//
// Calling this is optional: until it runs, logging goes nowhere rather than
// panicking on a nil logger.
func InitLogger(debug bool) {
	logger = build(config(debug))
}

// InitFileLogger sends logging to a file instead of the terminal, and returns
// the path it settled on.
//
// The terminal UI owns the screen. Anything written to stderr while it is
// running lands on top of the drawn cells, and because the display only
// repaints what it thinks has changed, the damage persists rather than being
// cleaned up on the next frame. Connecting an adapter emits several lines at
// once, which is why the corruption appeared exactly then.
//
// A failure here is not fatal: losing the log is better than refusing to run.
func InitFileLogger(debug bool, path string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	// Truncate rather than append: this is a diagnostic for the session in
	// progress, not an audit trail.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}

	cfg := config(debug)
	encoder := zapcore.NewJSONEncoder(cfg.EncoderConfig)
	if debug {
		encoder = zapcore.NewConsoleEncoder(cfg.EncoderConfig)
	}

	logger = zap.New(zapcore.NewCore(encoder, zapcore.AddSync(f), cfg.Level))
	return path, nil
}

// Discard silences logging, for when there is nowhere safe to write.
func Discard() {
	logger = zap.NewNop()
}

func build(cfg zap.Config) *zap.Logger {
	l, err := cfg.Build()
	if err != nil {
		panic(err)
	}
	return l
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
