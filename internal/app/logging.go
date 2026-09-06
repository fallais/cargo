package app

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// logFile holds the open log so CloseLog can release it.
var logFile io.Closer

func logLevel(debug bool) slog.Level {
	if debug {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

// InitLogging sends logging to stderr, for anything that does not take over
// the terminal.
func InitLogging(debug bool) {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel(debug),
	})))
}

// LogToFile sends logging to a file and returns the path.
//
// The UI owns the screen: a line written to stderr lands on the drawn cells,
// and since the display only repaints what changed, the damage persists.
func LogToFile(debug bool, path string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	// Truncate: a diagnostic for this session, not an audit trail.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}

	logFile = f
	slog.SetDefault(slog.New(slog.NewJSONHandler(f, &slog.HandlerOptions{
		Level: logLevel(debug),
	})))
	return path, nil
}

// DiscardLogging silences logging, for when there is nowhere safe to write.
func DiscardLogging() {
	logFile = nil
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// CloseLog releases the log file. slog writes through unbuffered, so there is
// nothing to flush, but the file still has to be closed.
func CloseLog() {
	if logFile != nil {
		_ = logFile.Close()
		logFile = nil
	}
}

// LogPath is where the UI writes its log, since it cannot use the terminal.
func LogPath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "cargo.log")
	}
	return filepath.Join(dir, "cargo", "cargo.log")
}
