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

// maxSession caps how long one dashboard session may stay continuously
// connected. When the cap is reached every live connection is force-closed
// so the process can fall back into deep sleep (or exit under EXIT_ON_IDLE
// and let socket activation re-arm); clients transparently reconnect on
// their next poll, opening a fresh session.
const maxSession = 4 * time.Minute

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
	liveConns  map[net.Conn]struct{}
	sess       *time.Timer
	maxSession time.Duration
	sleeping   bool
	timer      *time.Timer
	awakeProcs int
	awakeGC    int
	grace      time.Duration
	exitOnIdle bool
	exit       func()
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
		liveConns:  make(map[net.Conn]struct{}),
		maxSession: maxSession,
	}
	if wakeFn == nil {
		wakeFn = func() { runtimeWake(g.awakeProcs, g.awakeGC) }
	}
	if sleepFn == nil {
		sleepFn = runtimeSleep
	}
	g.wakeFn, g.sleepFn = wakeFn, sleepFn
	g.exit = func() { os.Exit(0) }
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
func IdleConnState(c net.Conn, state http.ConnState) { sessions.connState(c, state) }

// BootSleep puts the process into deep sleep before the listener accepts,
// so a freshly started zdash binds its socket without touching zpool,
// smartctl, /proc or anything else. Call exactly once from main.
func BootSleep() { sessions.enterSleep() }

// ExitOnIdle terminates the process (status 0) once the last session has
// closed and the idle grace has passed, instead of staying parked in deep
// sleep. Intended for systemd socket activation, where the .socket unit
// restarts the service on demand so idle RAM and CPU drop to zero.
// The boot-time BootSleep park is never terminal: the pending connection
// that socket activation started the service for is always served first.
func ExitOnIdle() {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	sessions.exitOnIdle = true
}

// connState maps http.ConnState transitions onto the connection counter.
// A connection is counted from StateNew (accept) through StateActive /
// StateIdle until it is Closed or Hijacked.
//
// Counting stays atomic and lock-free on purpose: the counter must be
// visible to trySleep even while a newly accepted connection is still
// blocked on the gate mutex in wake(), otherwise a live connection could
// be missed and the process parked (or exited in exit-on-idle mode)
// underneath it.
func (g *idleGate) connState(c net.Conn, state http.ConnState) {
	switch state {
	case http.StateNew:
		// Count first, map second: the counter must be visible to
		// trySleep even while this call is blocked on g.mu below.
		if g.conns.Add(1) == 1 {
			g.wake()
		}
		g.mu.Lock()
		g.liveConns[c] = struct{}{}
		g.mu.Unlock()
	case http.StateClosed, http.StateHijacked:
		if g.conns.Add(-1) == 0 {
			g.scheduleSleep()
		}
		g.mu.Lock()
		delete(g.liveConns, c)
		g.mu.Unlock()
	}
}

// armSessionLocked arms the session cap when the gate is awake but no
// window is pending. Called with g.mu held. An already-armed timer is
// left untouched so a continuously connected session can never extend
// past maxSession, while any session that is still exchanging data gets
// its own full window: a client that reconnects after (or during) a
// forced session end must never leave the process awake and uncapped.
func (g *idleGate) armSessionLocked() {
	if g.sess == nil && !g.sleeping {
		g.sess = time.AfterFunc(g.maxSession, g.endSession)
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
		// Already awake (a reconnect inside the idle grace, or right
		// after a forced session end): re-open a session window so the
		// active endpoint stays served, but stays capped.
		g.armSessionLocked()
		return
	}
	g.sleeping = false
	log.Printf("☀️  session opened — awake")
	// Arm the session cap: whatever keeps its connection alive (proxies,
	// forgetful clients) gets disconnected after maxSession so the process
	// can park and socket activation can re-arm.
	g.armSessionLocked()
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
	if g.sess != nil {
		g.sess.Stop()
		g.sess = nil
	}
	if g.sleeping || g.conns.Load() != 0 {
		return
	}
	g.sleeping = true
	log.Printf("💤 no active session — deep sleep")
	g.sleepFn()
	// sleepFn (notably FreeOSMemory) can take a while, and a fresh
	// connection may have been accepted and counted while we ran: its
	// wake() is blocked on g.mu right now and would undo the park, so
	// never exit on top of a live connection.
	if g.exitOnIdle && g.conns.Load() == 0 {
		log.Printf("💤 idle — exiting (socket activation restarts on demand)")
		g.exit()
	}
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
	if g.sess != nil {
		g.sess.Stop()
		g.sess = nil
	}
	if g.sleeping {
		return
	}
	g.sleeping = true
	g.sleepFn()
}

// endSession is the maxSession timer callback: force-close every live
// connection so the gate falls back into deep sleep (and, under
// EXIT_ON_IDLE, exits so socket activation takes over). The server
// reports each closed connection via the ConnState hook, which drives the
// counter to zero and arms the regular idle grace.
func (g *idleGate) endSession() {
	g.mu.Lock()
	g.sess = nil
	conns := make([]net.Conn, 0, len(g.liveConns))
	for c := range g.liveConns {
		conns = append(conns, c)
	}
	clear(g.liveConns)
	g.mu.Unlock()
	if len(conns) == 0 {
		return
	}
	log.Printf("⏳  session reached %s cap — closing %d connection(s) to park", g.maxSession, len(conns))
	for _, c := range conns {
		if c != nil {
			c.Close()
		}
	}
	// A fresh connection may have been accepted while the old ones were
	// being closed (or a client may have reconnected already, without the
	// counter ever dropping to zero and re-running wake). That endpoint is
	// active and must stay served — but only within a new capped window,
	// never awake without a session cap.
	g.mu.Lock()
	g.armSessionLocked()
	g.mu.Unlock()
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
		// Drop the cache first: runtimeSleep's FreeOSMemory can only
		// return those pages to the OS once the refresher released them.
		cache.setActive(false)
		runtimeSleepFn()
	}
}
