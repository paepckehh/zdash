package zdash

import (
	"context"
	"net/http"
	"net/http/httptest"
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

	// Sleep drops the cache and stops the loop.
	r.mu.Lock()
	if len(r.entries) != 0 {
		t.Error("cache not dropped on deactivate")
	}
	r.mu.Unlock()
	after := calls.Load()
	time.Sleep(40 * time.Millisecond)
	if got := calls.Load(); got != after {
		t.Errorf("producer calls = %d, want %d (loop stopped)", got, after)
	}
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
