package zdash

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

// Per-endpoint cache lifetimes. zpool/arc/sysinfo slightly exceed the
// dashboard's 5s poll cadence so the background loop is the only fetcher;
// SMART probing is expensive (one smartctl process per drive) and its data
// barely moves, so it is refreshed far less often while still being served
// instantly from cache on every poll.
const (
	zpoolTTL   = 6 * time.Second
	arcTTL     = 6 * time.Second
	sysinfoTTL = 6 * time.Second
	smartTTL   = 60 * time.Second

	// refreshTick is the cadence of the background refresh loop while a
	// session is active.
	refreshTick = 5 * time.Second
)

// producer renders one endpoint's JSON response body. It owns its own
// timeout via the passed context and returns the exact bytes plus HTTP
// status that get cached and served verbatim.
type producer func(ctx context.Context) (body []byte, status int)

// defaultProducers registers one producer per API endpoint key.
var defaultProducers = map[string]producer{
	"zpool":   produceZpool,
	"arc":     produceARC,
	"sysinfo": produceSysInfo,
	"smart":   produceSmart,
}

// refreshEntry is one cached endpoint response.
type refreshEntry struct {
	body    []byte
	status  int
	fetched time.Time
}

// fetchCall deduplicates concurrent refreshes of the same endpoint: the
// first caller runs the producer, everyone else waits on done.
type fetchCall struct {
	done   chan struct{}
	body   []byte
	status int
}

// refresher is the session-scoped background fetcher. While a session is
// active it refreshes every endpoint on its TTL schedule so the HTTP
// handlers only ever copy cached bytes (one zpool/smartctl run per
// interval, no matter how many dashboard tabs are open). When the session
// ends it stops the loop and drops the cache so deep sleep can hand the
// memory back to the OS.
type refresher struct {
	mu        sync.Mutex
	ttls      map[string]time.Duration
	producers map[string]producer
	tick      time.Duration
	entries   map[string]*refreshEntry
	inflight  map[string]*fetchCall
	active    bool
	gen       uint64
	stop      chan struct{}
}

// cache is the process-wide refresher instance wired into the idle gate
// and shared by all API handlers.
var cache = newRefresher()

func newRefresher() *refresher {
	return &refresher{
		ttls: map[string]time.Duration{
			"zpool":   zpoolTTL,
			"arc":     arcTTL,
			"sysinfo": sysinfoTTL,
			"smart":   smartTTL,
		},
		producers: defaultProducers,
		tick:      refreshTick,
		entries:   make(map[string]*refreshEntry),
		inflight:  make(map[string]*fetchCall),
	}
}

// setActive starts or stops the background refresh loop.
func (r *refresher) setActive(active bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if active {
		if r.active {
			return
		}
		r.active = true
		r.stop = make(chan struct{})
		go r.loop(r.stop)
		return
	}
	r.active = false
	r.gen++
	if r.stop != nil {
		// The channel stays non-nil (closed): get() uses its closed
		// state to refuse fetches that a committed tick tries to start
		// after the session already ended. Never close twice.
		select {
		case <-r.stop:
		default:
			close(r.stop)
		}
	}
	// Drop cached bodies; runtimeSleep's debug.FreeOSMemory hands the
	// released pages back to the OS.
	clear(r.entries)
}

// loop refreshes all stale endpoints immediately and then on every tick,
// until the session goes idle.
func (r *refresher) loop(stop chan struct{}) {
	r.refreshAll()
	t := time.NewTicker(r.tick)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			// A tick and a concurrent deactivate can both be ready; never
			// start a refresh round on a session that is already gone.
			select {
			case <-stop:
				return
			default:
			}
			r.refreshAll()
		}
	}
}

// refreshAll refreshes every endpoint whose cached response has expired.
func (r *refresher) refreshAll() {
	r.mu.Lock()
	keys := make([]string, 0, len(r.ttls))
	for key, ttl := range r.ttls {
		if e := r.entries[key]; e == nil || time.Since(e.fetched) >= ttl {
			keys = append(keys, key)
		}
	}
	r.mu.Unlock()
	var wg sync.WaitGroup
	for _, key := range keys {
		wg.Go(func() { r.get(context.Background(), key) })
	}
	wg.Wait()
}

// get returns the cached response for key, refreshing it when stale. The
// first caller runs the producer; concurrent callers (background loop plus
// any number of HTTP handlers) share that single fetch.
func (r *refresher) get(ctx context.Context, key string) ([]byte, int) {
	r.mu.Lock()
	ttl := r.ttls[key]
	if e := r.entries[key]; e != nil && time.Since(e.fetched) < ttl {
		r.mu.Unlock()
		return e.body, e.status
	}
	if c := r.inflight[key]; c != nil {
		r.mu.Unlock()
		return r.wait(ctx, key, c)
	}
	p := r.producers[key]
	if p == nil {
		r.mu.Unlock()
		return errBody("unknown endpoint"), http.StatusNotFound
	}
	// Authoritative session check, serialized with setActive's gen bump:
	// once the session's stop channel is closed, no new fetch may start (a
	// tick that was already committed when the session ended lands here).
	if r.stop != nil {
		select {
		case <-r.stop:
			r.mu.Unlock()
			return errBody("session ended"), http.StatusServiceUnavailable
		default:
		}
	}
	call := &fetchCall{done: make(chan struct{})}
	gen := r.gen
	r.inflight[key] = call
	r.mu.Unlock()

	// The fetch outlives whichever request triggered it, so other
	// waiters and the cache still get a result if that client disconnects.
	go func() {
		defer func() {
			r.mu.Lock()
			delete(r.inflight, key)
			r.mu.Unlock()
			close(call.done)
		}()
		body, status := p(context.WithoutCancel(ctx))
		r.mu.Lock()
		// Store only into the session that started the fetch: if the
		// session ended mid-fetch the cache was already dropped, and a
		// late write would smuggle stale bytes into the next session.
		if gen == r.gen {
			r.entries[key] = &refreshEntry{body: body, status: status, fetched: time.Now()}
		}
		r.mu.Unlock()
		call.body, call.status = body, status
	}()
	return r.wait(ctx, key, call)
}

// wait blocks for an in-flight fetch, falling back to the newest stale
// snapshot if the caller's context goes away first.
func (r *refresher) wait(ctx context.Context, key string, c *fetchCall) ([]byte, int) {
	select {
	case <-c.done:
		return c.body, c.status
	case <-ctx.Done():
		r.mu.Lock()
		if e := r.entries[key]; e != nil {
			r.mu.Unlock()
			return e.body, e.status
		}
		r.mu.Unlock()
		return errBody("timed out waiting for background refresh"), http.StatusServiceUnavailable
	}
}

// flush drops all cached responses (test seam for handler isolation).
func (r *refresher) flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.entries)
}

// errBody renders a small JSON error body.
func errBody(msg string) []byte {
	b, _ := json.Marshal(map[string]string{"error": msg})
	return b
}

// serveRefresh is the shared handler body of every API endpoint: serve the
// (possibly freshly refreshed) cached JSON for key.
func serveRefresh(w http.ResponseWriter, r *http.Request, key string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, status := cache.get(r.Context(), key)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// produceZpool runs `zpool list -v --json` and renders the API response.
func produceZpool(ctx context.Context) ([]byte, int) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	data, err := fetchZpool(ctx, zpoolResolver())
	if err != nil {
		log.Printf("⚠️  zpool fetch failed: %v", err)
		return errBody("Failed to execute zpool command. Ensure it's installed and you have sufficient permissions (usually root or zfs group)."), http.StatusInternalServerError
	}
	body, err := json.Marshal(data)
	if err != nil {
		log.Printf("⚠️  zpool encode failed: %v", err)
		return errBody("Failed to encode zpool output."), http.StatusInternalServerError
	}
	return body, http.StatusOK
}
