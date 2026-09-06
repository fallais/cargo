package serial

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Connecting to a car is not something to do behind the user's back, so Start
// must not attach on its own unless it was asked to.
func TestStartDoesNotConnectWithoutAutoconnect(t *testing.T) {
	s := New(Options{Port: "/nonexistent/tty"})

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(s.Stop)

	if s.IsConnected() {
		t.Error("Start attached without being asked")
	}
	if s.Autoconnect() {
		t.Error("autoconnect defaulted to on")
	}
}

// With autoconnect on, a missing adapter has to fail promptly rather than hang.
func TestStartWithAutoconnectFailsPromptly(t *testing.T) {
	s := New(Options{Port: "/nonexistent/tty"})
	s.SetAutoconnect(true)

	done := make(chan error, 1)
	go func() { done <- s.Start(context.Background()) }()

	select {
	case err := <-done:
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

// Connect is the explicit path the adapter page uses.
func TestConnectReportsFailure(t *testing.T) {
	s := New(Options{})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)

	if err := s.Connect(context.Background(), "/nonexistent/tty"); err == nil {
		t.Error("Connect succeeded against a device that does not exist")
	}
	if s.IsConnected() {
		t.Error("IsConnected reported true after a failed connect")
	}
}

// Regression: the supervisor used to inherit the caller's startup timeout, so
// reconnection stopped a minute after launch and an adapter plugged in later
// was never noticed.
func TestSupervisorOutlivesAttemptDeadline(t *testing.T) {
	s := New(Options{Port: "/nonexistent/tty"})
	s.SetAutoconnect(true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx); err == nil {
		t.Fatal("Start succeeded with no adapter")
	}

	s.mu.RLock()
	running := s.stop != nil
	s.mu.RUnlock()

	if !running {
		t.Error("supervisor stopped as soon as the first attempt finished")
	}
}

// A user who disconnects deliberately must stay disconnected.
func TestDisconnectIsSafeAndSticks(t *testing.T) {
	s := New(Options{Port: "/nonexistent/tty"})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)

	s.Disconnect()
	s.Disconnect()

	if s.IsConnected() {
		t.Error("still connected after Disconnect")
	}
	if s.Autoconnect() {
		t.Error("Disconnect turned autoconnect on")
	}
}

func TestAutoconnectToggles(t *testing.T) {
	s := New(Options{})
	if s.Autoconnect() {
		t.Error("autoconnect defaulted to on")
	}
	s.SetAutoconnect(true)
	if !s.Autoconnect() {
		t.Error("SetAutoconnect(true) did not take")
	}
	s.SetAutoconnect(false)
	if s.Autoconnect() {
		t.Error("SetAutoconnect(false) did not take")
	}
}

// Stop is called from a defer and from the UI's shutdown path.
func TestStopIsIdempotent(t *testing.T) {
	s := New(Options{Port: "/nonexistent/tty"})
	s.Stop()
	s.Stop()

	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Stop()
	s.Stop()
}

// A configured port is offered alone; otherwise every plausible device is.
func TestAdaptersHonoursConfiguredPort(t *testing.T) {
	s := New(Options{Port: "/dev/ttyUSB9"})
	got := s.Adapters()

	if len(got) != 1 || got[0].Port != "/dev/ttyUSB9" {
		t.Errorf("Adapters() = %+v, want just the configured port", got)
	}
}

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
