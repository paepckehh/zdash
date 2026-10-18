package zdash

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestRefresher builds a refresher with a single test key whose producer
// counts invocations and returns the given body/status.
func newTestRefresher(t *testing.T, key string, ttl time.Duration, body []byte, status int) (*refresher, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	r := &refresher{
		ttls: map[string]time.Duration{key: ttl},
		producers: map[string]producer{
			key: func(context.Context) ([]byte, int) {
				calls.Add(1)
				return body, status
			},
		},
		tick:     10 * time.Millisecond,
		entries:  make(map[string]*refreshEntry),
		inflight: make(map[string]*fetchCall),
	}
	return r, &calls
}

func TestRefresher_GetCachesWithinTTL(t *testing.T) {
	r, calls := newTestRefresher(t, "k", time.Hour, []byte(`{"ok":true}`), 200)

	for range 3 {
		body, status := r.get(context.Background(), "k")
		if status != 200 || string(body) != `{"ok":true}` {
			t.Fatalf("get = %q/%d, want {\"ok\":true}/200", body, status)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("producer calls = %d, want 1 (TTL cache)", got)
	}

	r.flush()
	r.get(context.Background(), "k")
	if got := calls.Load(); got != 2 {
		t.Errorf("producer calls after flush = %d, want 2", got)
	}
}

func TestRefresher_GetExpiresAfterTTL(t *testing.T) {
	r, calls := newTestRefresher(t, "k", 10*time.Millisecond, []byte(`{}`), 200)

	r.get(context.Background(), "k")
	time.Sleep(20 * time.Millisecond)
	r.get(context.Background(), "k")
	if got := calls.Load(); got != 2 {
		t.Errorf("producer calls = %d, want 2 (entry expired)", got)
	}
}

func TestRefresher_ConcurrentGetSingleFlight(t *testing.T) {
	body := []byte(`{"slow":true}`)
	var calls atomic.Int64
	r := &refresher{
		ttls: map[string]time.Duration{"k": time.Hour},
		producers: map[string]producer{
			"k": func(ctx context.Context) ([]byte, int) {
				calls.Add(1)
				time.Sleep(30 * time.Millisecond)
				return body, 200
			},
		},
		tick:     time.Hour,
		entries:  make(map[string]*refreshEntry),
		inflight: make(map[string]*fetchCall),
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 16 {
		wg.Go(func() {
			<-start
			b, status := r.get(context.Background(), "k")
			if status != 200 || string(b) != `{"slow":true}` {
				t.Errorf("get = %q/%d", b, status)
			}
		})
	}
	close(start)
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Errorf("producer calls = %d, want 1 (singleflight)", got)
	}
}

func TestRefresher_BackgroundLoopRefreshesAndStops(t *testing.T) {
	var calls atomic.Int64
	r := &refresher{
		ttls: map[string]time.Duration{"k": 5 * time.Millisecond},
		producers: map[string]producer{
			"k": func(context.Context) ([]byte, int) {
				calls.Add(1)
				return []byte(`{}`), 200
			},
		},
		tick:     5 * time.Millisecond,
		entries:  make(map[string]*refreshEntry),
		inflight: make(map[string]*fetchCall),
	}

	r.setActive(true)
	// The immediate refreshAll plus a few ticks must have run.
	time.Sleep(40 * time.Millisecond)
	r.setActive(false)
	if got := calls.Load(); got < 2 {
		t.Errorf("producer calls = %d, want >= 2 (loop ran)", got)
	}

	// Sleep drops the cache and stops the loop. A tick that was already
	// committed when setActive(false) ran may finish, so treat the call
	// count as settled once it stops changing for well over a full tick.
	r.mu.Lock()
	if len(r.entries) != 0 {
		t.Error("cache not dropped on deactivate")
	}
	r.mu.Unlock()
	stableCalls(t, &calls)
	r.mu.Lock()
	if len(r.entries) != 0 {
		t.Error("cache repopulated after deactivate")
	}
	r.mu.Unlock()
}

// stableCalls waits until the producer call count has not changed for a
// quiet period longer than one refresh tick, which proves the loop stopped.
func stableCalls(t *testing.T, calls *atomic.Int64) int64 {
	t.Helper()
	quiet := 80 * time.Millisecond
	deadline := time.Now().Add(5 * time.Second)
	last := calls.Load()
	for {
		time.Sleep(quiet)
		cur := calls.Load()
		if cur == last {
			return cur
		}
		last = cur
		if time.Now().After(deadline) {
			break
		}
	}
	t.Fatalf("producer calls never settled, last = %d", last)
	return last
}

// An in-flight fetch that completes after the session ended must not
// smuggle its (now stale) body back into the dropped cache.
func TestRefresher_InflightFetchNotCachedAfterDeactivate(t *testing.T) {
	r, calls := newTestRefresher(t, "k", time.Hour, []byte(`{}`), 200)
	// Slow the producer so the fetch overlaps the deactivate.
	r.producers["k"] = func(ctx context.Context) ([]byte, int) {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		return []byte(`{"late":true}`), 200
	}

	r.setActive(true)
	// Kick off a fetch and wait until it is in flight.
	var wg sync.WaitGroup
	wg.Go(func() { r.get(context.Background(), "k") })
	for {
		r.mu.Lock()
		inflight := r.inflight["k"] != nil
		r.mu.Unlock()
		if inflight {
			break
		}
		time.Sleep(time.Millisecond)
	}
	r.setActive(false)

	r.mu.Lock()
	if len(r.entries) != 0 {
		t.Error("cache not empty after deactivate")
	}
	r.mu.Unlock()
	wg.Wait()
	time.Sleep(20 * time.Millisecond)
	r.mu.Lock()
	if len(r.entries) != 0 {
		t.Error("in-flight fetch repopulated the dropped cache")
	}
	r.mu.Unlock()
}

func TestRefresher_WaiterGets503OnContextCancelWithoutSnapshot(t *testing.T) {
	var calls atomic.Int64
	r := &refresher{
		ttls: map[string]time.Duration{"k": time.Hour},
		producers: map[string]producer{
			"k": func(context.Context) ([]byte, int) {
				calls.Add(1)
				time.Sleep(50 * time.Millisecond)
				return []byte(`{}`), 200
			},
		},
		tick:     time.Hour,
		entries:  make(map[string]*refreshEntry),
		inflight: make(map[string]*fetchCall),
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		r.get(ctx, "k")
	}()
	time.Sleep(5 * time.Millisecond)
	r.mu.Lock()
	c := r.inflight["k"]
	r.mu.Unlock()
	if c == nil {
		t.Fatal("no in-flight call registered")
	}
	cancel()
	body, status := r.wait(ctx, "k", c)
	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", status)
	}
	if len(body) == 0 {
		t.Error("expected error body")
	}
}

func TestServeRefresh_MethodNotAllowed(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/api/arc", nil)
		rec := httptest.NewRecorder()
		HandleARCAPI(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status = %d, want 405", method, rec.Code)
		}
	}
}

func TestServeRefresh_UnknownEndpoint(t *testing.T) {
	// An unknown endpoint key must answer 404 with a JSON error body and
	// the shared JSON content type, not 500 and not a plain-text body.
	req := httptest.NewRequest(http.MethodGet, "/api/nope", nil)
	rec := httptest.NewRecorder()
	serveRefresh(rec, req, "nope")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json*", ct)
	}
	if !strings.Contains(rec.Body.String(), `"error"`) {
		t.Errorf("body %q missing error key", rec.Body.String())
	}
}

func TestRefresher_GetAfterSessionEndedAnswers503(t *testing.T) {
	// Once a session ends (stop channel closed, cache dropped) a handler
	// that still lands on get() must not start a new fetch: it answers
	// 503 so the client retries on the next session instead.
	r, calls := newTestRefresher(t, "k", time.Hour, []byte(`{}`), 200)
	r.setActive(true)
	r.setActive(false)

	body, status := r.get(context.Background(), "k")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", status)
	}
	if !strings.Contains(string(body), "session ended") {
		t.Errorf("body = %q, want the session-ended error", body)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("producer calls = %d, want 0 (no fetch after session end)", got)
	}
}

func TestRefresher_WaiterFallsBackToStaleSnapshot(t *testing.T) {
	// A caller whose context dies while a fetch is in flight must fall
	// back to the newest stale snapshot instead of a bare 503.
	var calls atomic.Int64
	r := &refresher{
		ttls: map[string]time.Duration{"k": time.Hour},
		producers: map[string]producer{
			"k": func(context.Context) ([]byte, int) {
				calls.Add(1)
				return []byte(`{"stale":true}`), 200
			},
		},
		tick:     time.Hour,
		entries:  make(map[string]*refreshEntry),
		inflight: make(map[string]*fetchCall),
	}

	// Populate the cache with a snapshot...
	r.get(context.Background(), "k")
	// ...then backdate it so it is stale, and swap in a slow producer so
	// the next get() goes in flight long enough for the cancel to race in.
	r.mu.Lock()
	r.entries["k"].fetched = time.Now().Add(-time.Hour)
	r.producers["k"] = func(context.Context) ([]byte, int) {
		calls.Add(1)
		time.Sleep(80 * time.Millisecond)
		return []byte(`{"fresh":true}`), 200
	}
	r.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	go func() { r.get(ctx, "k") }()
	var c *fetchCall
	for {
		r.mu.Lock()
		c = r.inflight["k"]
		r.mu.Unlock()
		if c != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()

	body, status := r.wait(ctx, "k", c)
	if status != 200 {
		t.Fatalf("status = %d, want 200 (stale snapshot)", status)
	}
	if string(body) != `{"stale":true}` {
		t.Errorf("body = %q, want the stale snapshot", body)
	}
}
