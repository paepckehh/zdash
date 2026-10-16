package zdash

import (
	"net/http"
	"sync"
	"testing"
	"time"
)

// newTestGate builds an idleGate with a short grace period and transition
// callbacks that record their calls in order.
func newTestGate(t *testing.T) (*idleGate, *recorder) {
	t.Helper()
	rec := &recorder{}
	g := newIdleGate(rec.wake, rec.sleep)
	g.grace = 20 * time.Millisecond
	return g, rec
}

type recorder struct {
	mu    sync.Mutex
	order []string
}

func (r *recorder) record(event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.order = append(r.order, event)
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

func (r *recorder) wake()  { r.record("wake") }
func (r *recorder) sleep() { r.record("sleep") }

// waitEvent blocks until the recorder has n events or times out.
func (r *recorder) waitEvent(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(r.snapshot()) >= n {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d gate events, got %v", n, r.snapshot())
}

func TestIdleGate_BootSleep(t *testing.T) {
	g, rec := newTestGate(t)
	g.enterSleep()
	rec.waitEvent(t, 1)
	if got := rec.snapshot(); got[0] != "sleep" {
		t.Fatalf("boot sleep events = %v, want [sleep]", got)
	}
	// A second enterSleep must not fire the callback again.
	g.enterSleep()
	time.Sleep(30 * time.Millisecond)
	if got := len(rec.snapshot()); got != 1 {
		t.Errorf("duplicate boot sleep fired callback, events = %d", got)
	}
}

func TestIdleGate_WakeOnFirstConnection(t *testing.T) {
	g, rec := newTestGate(t)
	g.enterSleep()
	rec.waitEvent(t, 1)

	// A second connection must not fire a second wake.
	g.connState(http.StateNew)
	g.connState(http.StateNew)
	rec.waitEvent(t, 2)
	if got := rec.snapshot(); !equalStrings(got, []string{"sleep", "wake"}) {
		t.Fatalf("events = %v, want [sleep wake]", got)
	}
	if g.conns.Load() != 2 {
		t.Errorf("conns = %d, want 2", g.conns.Load())
	}
}

func TestIdleGate_SleepAfterGrace(t *testing.T) {
	g, rec := newTestGate(t)
	g.enterSleep()
	rec.waitEvent(t, 1)

	g.connState(http.StateNew)
	rec.waitEvent(t, 2)
	g.connState(http.StateClosed)
	// Still inside the grace window: no sleep yet.
	time.Sleep(5 * time.Millisecond)
	if got := rec.snapshot(); !equalStrings(got, []string{"sleep", "wake"}) {
		t.Fatalf("events = %v, want [sleep wake] during grace", got)
	}
	rec.waitEvent(t, 3)
	if got := rec.snapshot(); !equalStrings(got, []string{"sleep", "wake", "sleep"}) {
		t.Fatalf("events = %v, want [sleep wake sleep]", got)
	}
	if g.conns.Load() != 0 {
		t.Errorf("conns = %d, want 0", g.conns.Load())
	}
}

func TestIdleGate_ReconnectWithinGraceCancelsSleep(t *testing.T) {
	g, rec := newTestGate(t)
	g.enterSleep()
	rec.waitEvent(t, 1)

	g.connState(http.StateNew)
	rec.waitEvent(t, 2)
	g.connState(http.StateClosed)
	// Reconnect before the grace timer fires.
	g.connState(http.StateNew)
	time.Sleep(80 * time.Millisecond)
	if got := rec.snapshot(); !equalStrings(got, []string{"sleep", "wake"}) {
		t.Fatalf("events = %v, want [sleep wake] (reconnect must cancel sleep)", got)
	}
	// Closing again re-arms the timer and then sleeps.
	g.connState(http.StateClosed)
	rec.waitEvent(t, 3)
	if got := rec.snapshot(); !equalStrings(got, []string{"sleep", "wake", "sleep"}) {
		t.Fatalf("events = %v, want [sleep wake sleep]", got)
	}
}

func TestIdleGate_WakeSleepCycle(t *testing.T) {
	g, rec := newTestGate(t)
	g.enterSleep()

	n := 1 // the boot sleep
	for range 3 {
		g.connState(http.StateNew)
		rec.waitEvent(t, n+1) // wake
		g.connState(http.StateClosed)
		rec.waitEvent(t, n+2) // sleep after grace
		n += 2
	}
	want := []string{"sleep", "wake", "sleep", "wake", "sleep", "wake", "sleep"}
	if got := rec.snapshot(); !equalStrings(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestIdleGate_HijackedConnectionCounts(t *testing.T) {
	g, rec := newTestGate(t)
	g.enterSleep()
	rec.waitEvent(t, 1)

	g.connState(http.StateNew)
	rec.waitEvent(t, 2)
	g.connState(http.StateHijacked)
	rec.waitEvent(t, 3)
	if got := rec.snapshot(); !equalStrings(got, []string{"sleep", "wake", "sleep"}) {
		t.Fatalf("events = %v, want [sleep wake sleep]", got)
	}
}

func TestIdleGate_ExitOnIdle(t *testing.T) {
	g, rec := newTestGate(t)
	g.exitOnIdle = true
	exited := make(chan struct{}, 1)
	g.exit = func() { exited <- struct{}{} }
	g.enterSleep()
	rec.waitEvent(t, 1)

	// Boot-time parking must NOT be terminal, even in exit-on-idle mode:
	// the connection that socket activation started the process for is
	// always served first.
	select {
	case <-exited:
		t.Fatal("exit fired at boot park, before any session")
	default:
	}

	g.connState(http.StateNew)
	rec.waitEvent(t, 2)
	g.connState(http.StateClosed)
	// Deep sleep first, then the exit.
	rec.waitEvent(t, 3)
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("exit did not fire after idle deep sleep")
	}
	if got := rec.snapshot(); !equalStrings(got, []string{"sleep", "wake", "sleep"}) {
		t.Fatalf("events = %v, want [sleep wake sleep]", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
