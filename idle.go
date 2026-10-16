package zdash

import (
	"log"
	"net"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// idleGrace is how long the server waits after the last HTTP connection
// closes before falling back into deep sleep. It absorbs short reconnect
// gaps (page reloads, keep-alive races) so the runtime is not flipped in
// and out of sleep mode needlessly.
const idleGrace = 3 * time.Second

// idleGate tracks live HTTP connections (via the http.Server.ConnState
// hook) and drives the whole process between two modes:
//
//   - awake (session active): normal GC, full GOMAXPROCS parallelism, and
//     the background refresher keeping the API caches warm.
//   - deep sleep (no session): GC disabled, the heap handed back to the
//     OS, all but one OS thread parked. Only the listener socket stays
//     armed, so idle CPU and RAM drop to the absolute minimum while the
//     dashboard stays reachable. The first incoming connection instantly
//     flips the process back to awake mode.
type idleGate struct {
	mu         sync.Mutex
	conns      atomic.Int64
	sleeping   bool
	timer      *time.Timer
	awakeProcs int
	awakeGC    int
	grace      time.Duration
	wakeFn     func()
	sleepFn    func()
}

// newIdleGate builds a gate. wakeFn/sleepFn are the transition callbacks;
// nil selects the default runtime tuners (GC on/off, GOMAXPROCS restore,
// FreeOSMemory). Tests can inject their own callbacks to assert
// transitions without touching the real GC.
func newIdleGate(wakeFn, sleepFn func()) *idleGate {
	g := &idleGate{
		awakeProcs: runtime.GOMAXPROCS(0),
		awakeGC:    envGOGC(),
		grace:      idleGrace,
	}
	if wakeFn == nil {
		wakeFn = func() { runtimeWake(g.awakeProcs, g.awakeGC) }
	}
	if sleepFn == nil {
		sleepFn = runtimeSleep
	}
	g.wakeFn, g.sleepFn = wakeFn, sleepFn
	return g
}

// envGOGC returns the GC target percentage to use while awake: the GOGC
// environment variable when set ("off" maps to GC-off even while awake),
// otherwise the Go default of 100.
func envGOGC() int {
	switch v := os.Getenv("GOGC"); v {
	case "off":
		return -1
	case "":
		return 100
	default:
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		return 100
	}
}

// runtimeWake restores full parallelism and re-enables the GC for an
// active session.
func runtimeWake(procs, gc int) {
	runtime.GOMAXPROCS(procs)
	debug.SetGCPercent(gc)
}

// runtimeSleep is the deep-sleep profile: nothing allocates while no
// session is connected, so the GC can be disabled entirely, the heap can
// be scavenged back to the OS, and all but one OS thread parked.
func runtimeSleep() {
	debug.SetGCPercent(-1)
	debug.FreeOSMemory()
	runtime.GOMAXPROCS(1)
}

// IdleConnState is the http.Server.ConnState hook that tracks sessions.
// Wire it into http.Server.ConnState to get session-aware deep sleep.
func IdleConnState(_ net.Conn, state http.ConnState) { sessions.connState(state) }

// BootSleep puts the process into deep sleep before the listener accepts,
// so a freshly started zdash binds its socket without touching zpool,
// smartctl, /proc or anything else. Call exactly once from main.
func BootSleep() { sessions.enterSleep() }

// connState maps http.ConnState transitions onto the connection counter.
// A connection is counted from StateNew (accept) through StateActive /
// StateIdle until it is Closed or Hijacked.
func (g *idleGate) connState(state http.ConnState) {
	switch state {
	case http.StateNew:
		if g.conns.Add(1) == 1 {
			g.wake()
		}
	case http.StateClosed, http.StateHijacked:
		if g.conns.Add(-1) == 0 {
			g.scheduleSleep()
		}
	}
}

// wake leaves deep-sleep mode and arms the background refresher.
func (g *idleGate) wake() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.timer != nil {
		g.timer.Stop()
		g.timer = nil
	}
	if !g.sleeping {
		return
	}
	g.sleeping = false
	log.Printf("☀️  session opened — awake")
	g.wakeFn()
}

// scheduleSleep arms the grace timer that will put the process back into
// deep sleep once no connection has shown up for idleGrace.
func (g *idleGate) scheduleSleep() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sleeping || g.timer != nil {
		return
	}
	g.timer = time.AfterFunc(g.grace, g.trySleep)
}

// trySleep is the grace-timer callback.
func (g *idleGate) trySleep() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.timer = nil
	if g.sleeping || g.conns.Load() != 0 {
		return
	}
	g.sleeping = true
	log.Printf("💤 no active session — deep sleep")
	g.sleepFn()
}

// enterSleep puts the gate into deep sleep immediately, cancelling any
// pending grace timer. Used at boot and in tests.
func (g *idleGate) enterSleep() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.timer != nil {
		g.timer.Stop()
		g.timer = nil
	}
	if g.sleeping {
		return
	}
	g.sleeping = true
	g.sleepFn()
}

// sessions is the process-wide idle gate, wired into the HTTP server via
// IdleConnState. Its callbacks glue the runtime sleep profile and the
// background refresher together: waking up restores GC/parallelism and
// starts refreshing, sleeping parks everything again. The refresher hookup
// happens in init() to keep the initializer cycle-free.
var sessions = newIdleGate(nil, nil)

func init() {
	runtimeWakeFn, runtimeSleepFn := sessions.wakeFn, sessions.sleepFn
	sessions.wakeFn = func() {
		runtimeWakeFn()
		cache.setActive(true)
	}
	sessions.sleepFn = func() {
		runtimeSleepFn()
		cache.setActive(false)
	}
}
