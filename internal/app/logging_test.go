package app

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regression: logging went to stderr unconditionally, so every line written
// while the terminal UI held the screen landed on top of the drawn cells. The
// display only repaints what it believes has changed, so the damage stayed
// until something forced a full redraw.
func TestFileLoggerWritesToFileNotStderr(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "cargo.log")

	got, err := LogToFile(false, path)
	if err != nil {
		t.Fatalf("LogToFile: %v", err)
	}
	if got != path {
		t.Errorf("returned path %q, want %q", got, path)
	}
	t.Cleanup(DiscardLogging)

	slog.Info("adapter ready", "port", "/dev/ttyUSB0")
	CloseLog()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	if !strings.Contains(string(data), "adapter ready") {
		t.Errorf("log does not contain the message: %q", data)
	}
	if !strings.Contains(string(data), "/dev/ttyUSB0") {
		t.Errorf("log dropped the attributes: %q", data)
	}
}

// Debug lines are the ones that say which device and baud rate were tried, so
// the level has to actually take effect.
func TestDebugLevel(t *testing.T) {
	for _, tc := range []struct {
		debug bool
		want  bool
	}{{true, true}, {false, false}} {
		path := filepath.Join(t.TempDir(), "cargo.log")
		if _, err := LogToFile(tc.debug, path); err != nil {
			t.Fatal(err)
		}
		slog.Debug("port unavailable", "port", "/dev/ttyUSB0")
		CloseLog()

		data, _ := os.ReadFile(path)
		if got := strings.Contains(string(data), "port unavailable"); got != tc.want {
			t.Errorf("debug=%v: line present = %v, want %v", tc.debug, got, tc.want)
		}
	}
	DiscardLogging()
}

// A log that cannot be opened must not stop the tool running.
func TestFileLoggerReportsAnUnwritablePath(t *testing.T) {
	file := filepath.Join(t.TempDir(), "occupied")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	// A path underneath a regular file cannot be created.
	if _, err := LogToFile(false, filepath.Join(file, "cargo.log")); err == nil {
		t.Error("LogToFile accepted an impossible path")
	}
}

func TestDiscardAndCloseAreSafe(t *testing.T) {
	DiscardLogging()
	slog.Info("goes nowhere")
	CloseLog()
	CloseLog()
}
