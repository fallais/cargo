package log

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// Regression: logging went to stderr unconditionally, so every line written
// while the terminal UI held the screen landed on top of the drawn cells. The
// display only repaints what it believes has changed, so the damage stayed
// until something forced a full redraw.
func TestFileLoggerWritesToFileNotStderr(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "cargo.log")

	got, err := InitFileLogger(false, path)
	if err != nil {
		t.Fatalf("InitFileLogger: %v", err)
	}
	if got != path {
		t.Errorf("returned path %q, want %q", got, path)
	}
	t.Cleanup(func() { logger = zap.NewNop() })

	Info("adapter ready", zap.String("port", "/dev/ttyUSB0"))
	Sync()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	if !strings.Contains(string(data), "adapter ready") {
		t.Errorf("log does not contain the message: %q", data)
	}
	if !strings.Contains(string(data), "/dev/ttyUSB0") {
		t.Errorf("log dropped the fields: %q", data)
	}
}

// A log that cannot be opened must not stop the tool running.
func TestFileLoggerReportsAnUnwritablePath(t *testing.T) {
	file := filepath.Join(t.TempDir(), "occupied")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	// A path underneath a regular file cannot be created.
	if _, err := InitFileLogger(false, filepath.Join(file, "cargo.log")); err == nil {
		t.Error("InitFileLogger accepted an impossible path")
	}
}

// Logging before any Init must be silent rather than panic on a nil logger.
func TestLoggingBeforeInitIsSilent(t *testing.T) {
	saved := logger
	logger = zap.NewNop()
	t.Cleanup(func() { logger = saved })

	Info("no logger configured")
	Debug("nor here")
	Sync()
}

func TestDiscard(t *testing.T) {
	saved := logger
	t.Cleanup(func() { logger = saved })

	Discard()
	Info("goes nowhere")
	Sync()
}
