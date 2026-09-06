package serial

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Start must not hang when no adapter is present, which is the ordinary case
// on a machine with nothing plugged in.
func TestStartWithNoAdapterFailsPromptly(t *testing.T) {
	s := New(Options{Port: "/nonexistent/tty"})

	done := make(chan error, 1)
	go func() { done <- s.Start(context.Background()) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Start succeeded with no adapter")
		}
		if !errors.Is(err, ErrNoAdapter) {
			t.Errorf("err = %v, want ErrNoAdapter", err)
		}
	case <-time.After(connectTimeout + 5*time.Second):
		t.Fatal("Start did not return")
	}

	if s.IsConnected() {
		t.Error("IsConnected reported true after a failed start")
	}
	s.Stop()
}

// Regression: the supervisor used to inherit the caller's startup timeout, so
// reconnection stopped a minute after launch and an adapter plugged in later
// was never noticed. It must outlive any per-attempt deadline and stop only
// when the session context does.
func TestSupervisorOutlivesAttemptDeadline(t *testing.T) {
	s := New(Options{Port: "/nonexistent/tty"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx); err == nil {
		t.Fatal("Start succeeded with no adapter")
	}

	// The supervisor is still holding a cancel for the session, which Stop
	// is what releases. If Start had wired the supervisor to a deadline
	// instead, this would already be nil.
	s.mu.RLock()
	running := s.stop != nil
	s.mu.RUnlock()

	if !running {
		t.Error("supervisor stopped as soon as the first attempt finished")
	}
}

// Stop is called from a defer and from the UI's shutdown path, so it has to
// tolerate being called more than once and before any successful connection.
func TestStopIsIdempotent(t *testing.T) {
	s := New(Options{Port: "/nonexistent/tty"})
	s.Stop()
	s.Stop()

	if err := s.Start(context.Background()); err == nil {
		t.Fatal("Start succeeded with no adapter")
	}
	s.Stop()
	s.Stop()
}

// A configured port is used alone; without one every plausible device is
// tried, so a missing adapter is reported rather than a single guess failing.
func TestOptionsPorts(t *testing.T) {
	var explicit Options
	explicit.Port = "/dev/ttyUSB9"
	explicit.setDefaults()

	if got := explicit.ports(); len(got) != 1 || got[0] != "/dev/ttyUSB9" {
		t.Errorf("ports() = %v, want just the configured port", got)
	}

	var auto Options
	auto.setDefaults()
	if auto.ReadTimeout <= 0 {
		t.Error("setDefaults left no read timeout")
	}
}
