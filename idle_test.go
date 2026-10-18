package zdash

import (
	"net"
	"net/http"
	"runtime"
	"runtime/debug"
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

// A client that reconnects while the previous session window is being
// force-closed (or immediately after) must keep the process awake and
// serving — but never awake without a session cap: a fresh maxSession
// window is armed for it, so the 4-minute ceiling still holds.
func TestIdleGate_ReconnectAfterSessionEndStaysCapped(t *testing.T) {
	g, rec := newTestGate(t)
	g.maxSession = 30 * time.Millisecond
	g.enterSleep()
	rec.waitEvent(t, 1)

	// reportClosed runs a goroutine per pipe that reports StateClosed
	// once the gate's force-close lands on the connection.
	reportClosed := func(c net.Conn, closed chan<- struct{}) {
		go func(c net.Conn) {
			var b [1]byte
			c.SetReadDeadline(time.Now().Add(2 * time.Second))
			if _, err := c.Read(b[:]); err != nil {
				g.connState(c, http.StateClosed)
				closed <- struct{}{}
			}
		}(c)
	}
	closed1 := make(chan struct{}, 1)
	c1, _ := net.Pipe()
	reportClosed(c1, closed1)
	g.connState(c1, http.StateNew)
	rec.waitEvent(t, 2) // wake

	// Window one expires and force-closes c1. Reconnect immediately —
	// before the idle grace can park the gate — the way an actively
	// polling dashboard client does.
	<-closed1
	closed2 := make(chan struct{}, 1)
	c2, _ := net.Pipe()
	reportClosed(c2, closed2)
	g.connState(c2, http.StateNew)

	// The reconnect keeps the session online (no sleep during its
	// window) but must be capped by a fresh session window: c2 gets
	// force-closed too, the counter reaches zero, and the gate parks.
	rec.waitEvent(t, 3) // sleep after grace
	if got := rec.snapshot(); !equalStrings(got, []string{"sleep", "wake", "sleep"}) {
		t.Fatalf("events = %v, want [sleep wake sleep]", got)
	}
	if g.conns.Load() != 0 {
		t.Fatalf("conns = %d, want 0 after second forced close", g.conns.Load())
	}
	<-closed2
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

func TestEnvGOGC(t *testing.T) {
	cases := []struct {
		val  string
		want int
	}{
		{"", 100},
		{"off", -1},
		{"50", 50},
		{"1", 1},
		{"notanumber", 100},
	}
	for _, c := range cases {
		t.Setenv("GOGC", c.val)
		if got := envGOGC(); got != c.want {
			t.Errorf("envGOGC(%q) = %d, want %d", c.val, got, c.want)
		}
	}
}

func TestRuntimeWakeRestoresProfiles(t *testing.T) {
	// runtimeWake(runtimeSleep()) must restore GOMAXPROCS and re-enable
	// the GC with the configured target percent: the exact profile cycle
	// main() drives through BootSleep and the ConnState hook.
	procs := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(procs)
	if procs < 1 {
		procs = 1
	}
	runtimeSleep()
	runtimeWake(procs, 100)
	if got := runtime.GOMAXPROCS(0); got != procs {
		t.Errorf("GOMAXPROCS = %d, want %d", got, procs)
	}
	// The sleep profile's GC-off must be reversible; a freshly woken gate
	// must not inherit GC-off.
	runtimeSleep()
	if prev := debug.SetGCPercent(100); prev != -1 {
		t.Errorf("SetGCPercent after sleep = %d, want -1 (GC disabled)", prev)
	}
}

func TestIdleConnStateDelegatesToSessionsGate(t *testing.T) {
	// IdleConnState is the http.Server.ConnState hook wired in main; it
	// must drive the process-wide sessions gate. A wake/sleep cycle
	// through the hook must park the gate exactly as via the gate method.
	sessions.mu.Lock()
	prevGrace := sessions.grace
	prevWakeFn, prevSleepFn := sessions.wakeFn, sessions.sleepFn
	sessions.grace = 10 * time.Millisecond
	sessions.wakeFn = func() {}
	sessions.sleepFn = func() {}
	sessions.mu.Unlock()
	t.Cleanup(func() {
		sessions.mu.Lock()
		sessions.grace = prevGrace
		sessions.wakeFn, sessions.sleepFn = prevWakeFn, prevSleepFn
		if sessions.timer != nil {
			sessions.timer.Stop()
			sessions.timer = nil
		}
		sessions.mu.Unlock()
	})

	IdleConnState(nil, http.StateNew)
	if sessions.conns.Load() != 1 {
		t.Fatalf("conns = %d, want 1 after StateNew", sessions.conns.Load())
	}
	if sessions.sleeping {
		t.Fatal("gate still sleeping after StateNew")
	}
	IdleConnState(nil, http.StateClosed)
	if sessions.conns.Load() != 0 {
		t.Fatalf("conns = %d, want 0 after StateClosed", sessions.conns.Load())
	}
	// The grace timer parks the process-wide gate again.
	deadline := time.Now().Add(2 * time.Second)
	for {
		sessions.mu.Lock()
		parked := sessions.sleeping
		sessions.mu.Unlock()
		if parked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("gate did not park after the idle grace")
		}
		time.Sleep(time.Millisecond)
	}
}
