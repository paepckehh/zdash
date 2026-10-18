package zdash

import (
	"net"
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
	g.connState(nil, http.StateNew)
	g.connState(nil, http.StateNew)
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

	g.connState(nil, http.StateNew)
	rec.waitEvent(t, 2)
	g.connState(nil, http.StateClosed)
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

	g.connState(nil, http.StateNew)
	rec.waitEvent(t, 2)
	g.connState(nil, http.StateClosed)
	// Reconnect before the grace timer fires.
	g.connState(nil, http.StateNew)
	time.Sleep(80 * time.Millisecond)
	if got := rec.snapshot(); !equalStrings(got, []string{"sleep", "wake"}) {
		t.Fatalf("events = %v, want [sleep wake] (reconnect must cancel sleep)", got)
	}
	// Closing again re-arms the timer and then sleeps.
	g.connState(nil, http.StateClosed)
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
		g.connState(nil, http.StateNew)
		rec.waitEvent(t, n+1) // wake
		g.connState(nil, http.StateClosed)
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

	g.connState(nil, http.StateNew)
	rec.waitEvent(t, 2)
	g.connState(nil, http.StateHijacked)
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

	g.connState(nil, http.StateNew)
	rec.waitEvent(t, 2)
	g.connState(nil, http.StateClosed)
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

// A connection accepted while the grace-timer callback (trySleep) is
// mid-flight must never be lost: the connection is counted atomically
// before its wake() can take the gate mutex, so trySleep must see it and
// must not exit (exit-on-idle mode) on top of a live connection, and the
// blocked wake() must undo the park afterwards.
func TestIdleGate_ConnectionDuringSleepIsNotLost(t *testing.T) {
	inSleep := make(chan struct{}, 1)
	release := make(chan struct{})
	g := newIdleGate(nil, func() {
		inSleep <- struct{}{}
		<-release
	})
	g.grace = 10 * time.Millisecond
	g.exitOnIdle = true
	exited := make(chan struct{}, 1)
	g.exit = func() { exited <- struct{}{} }

	// Open and close a connection so the grace timer fires trySleep,
	// which then parks inside sleepFn holding the gate mutex.
	g.connState(nil, http.StateNew)
	g.connState(nil, http.StateClosed)
	<-inSleep

	// Reconnect accepted while trySleep is mid-flight: its wake() blocks
	// on g.mu until trySleep returns, but the count is already visible.
	woken := make(chan struct{})
	go func() {
		defer close(woken)
		g.connState(nil, http.StateNew)
	}()
	for i := 0; i < 1000 && g.conns.Load() == 0; i++ {
		time.Sleep(time.Millisecond)
	}
	if g.conns.Load() != 1 {
		t.Fatal("reconnect was not counted")
	}

	close(release) // let trySleep finish and evaluate the exit guard

	select {
	case <-exited:
		t.Fatal("exited while a live connection was pending wake")
	case <-woken:
	case <-time.After(2 * time.Second):
		t.Fatal("wake did not complete after trySleep released the gate")
	}
	if g.conns.Load() != 1 {
		t.Fatalf("conns = %d, want 1", g.conns.Load())
	}
	if g.sleeping {
		t.Fatal("gate still sleeping with live connections")
	}
}

// A session that stays connected past the session cap must be force-closed
// so the gate can park (and exit under exit-on-idle, letting socket
// activation re-arm) even when a client would otherwise hold its keep-alive
// connection open forever.
func TestIdleGate_MaxSessionForceClose(t *testing.T) {
	g, rec := newTestGate(t)
	g.maxSession = 30 * time.Millisecond
	g.exitOnIdle = true
	exited := make(chan struct{}, 1)
	g.exit = func() { exited <- struct{}{} }
	g.enterSleep()
	rec.waitEvent(t, 1)

	for range 2 {
		c, _ := net.Pipe()
		// Stand in for the http.Server: report StateClosed once the
		// force-close lands on the connection.
		go func(c net.Conn) {
			var b [1]byte
			c.SetReadDeadline(time.Now().Add(2 * time.Second))
			if _, err := c.Read(b[:]); err != nil {
				g.connState(c, http.StateClosed)
			}
		}(c)
		g.connState(c, http.StateNew)
	}
	rec.waitEvent(t, 2) // wake

	// Session exceeds the cap: both connections are closed, the counter
	// drops to zero, the gate parks, and exit-on-idle fires.
	rec.waitEvent(t, 3) // sleep after grace
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("exit did not fire after forced session end")
	}
	if g.conns.Load() != 0 {
		t.Fatalf("conns = %d, want 0 after forced close", g.conns.Load())
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
