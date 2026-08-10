package zdash

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// readARCSample loads testdata/arcstats, a representative capture of the
// /proc/spl/kstat/zfs/arcstats kstat file.
func readARCSample(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "arcstats"))
	if err != nil {
		t.Fatalf("read testdata/arcstats: %v", err)
	}
	return b
}

func TestParseARCStats_Sample(t *testing.T) {
	m, err := parseARCStats(readARCSample(t))
	if err != nil {
		t.Fatalf("parseARCStats: %v", err)
	}
	if m.Hits != 12345678 {
		t.Errorf("hits = %d, want 12345678", m.Hits)
	}
	if m.Misses != 876543 {
		t.Errorf("misses = %d, want 876543", m.Misses)
	}
	if m.Size != 2147483648 {
		t.Errorf("size = %d, want 2147483648", m.Size)
	}
	if m.MaxSize != 4294967296 {
		t.Errorf("c_max = %d, want 4294967296", m.MaxSize)
	}
	if m.L2Ndev != 0 {
		t.Errorf("l2_ndev = %d, want 0", m.L2Ndev)
	}
	if m.MFUHits != 11200000 {
		t.Errorf("mfu_hits = %d, want 11200000", m.MFUHits)
	}
	if m.DemandDataMisses != 240000 {
		t.Errorf("demand_data_misses = %d, want 240000", m.DemandDataMisses)
	}
	if m.MemoryAvailableBytes != 2147483648 {
		t.Errorf("memory_available_bytes = %d, want 2147483648",
			m.MemoryAvailableBytes)
	}
	if m.Deleted != 6789 {
		t.Errorf("deleted = %d, want 6789", m.Deleted)
	}
}

func TestParseARCStats_Truncated(t *testing.T) {
	// A one-line file (header only) must error, not panic.
	_, err := parseARCStats([]byte("12 1 0x01 0 0\n"))
	if err == nil {
		t.Fatal("expected error for truncated arcstats, got nil")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("err = %q, want a truncated error", err)
	}
}

func TestParseARCStats_Empty(t *testing.T) {
	in := []byte("12 1 0x01 0 0\nname  type  data\n")
	m, err := parseARCStats(in)
	if err != nil {
		t.Fatalf("parse empty: %v", err)
	}
	if m.Hits != 0 || m.Size != 0 {
		t.Errorf("expected zero metrics, got hits=%d size=%d",
			m.Hits, m.Size)
	}
}

func TestParseARCStats_MalformedValue(t *testing.T) {
	// A row with a non-numeric value must be skipped, not fatal.
	in := []byte("12 1 0x01 0 0\nname  type  data\nhits  4  notanumber\n")
	m, err := parseARCStats(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Hits != 0 {
		t.Errorf("expected hits=0 for malformed value, got %d", m.Hits)
	}
}

func TestParseARCStats_RoundTrip(t *testing.T) {
	var first ARCMetrics
	first, _ = parseRoundTrip(t)
	reencoded, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var second ARCMetrics
	if err := json.Unmarshal(reencoded, &second); err != nil {
		t.Fatalf("second unmarshal: %v", err)
	}
	if second != first {
		t.Errorf("round-trip mismatch: %+v != %+v", second, first)
	}
}

func parseRoundTrip(t *testing.T) (ARCMetrics, error) {
	t.Helper()
	m, err := parseARCStats(readARCSample(t))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return *m, nil
}

func TestFetchARC_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "arcstats")
	if err := os.WriteFile(path, readARCSample(t), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := fetchARC(ctx, path)
	if err != nil {
		t.Fatalf("fetchARC: %v", err)
	}
	if got.Hits != 12345678 {
		t.Errorf("hits = %d, want 12345678", got.Hits)
	}
	if got.Timestamp == 0 {
		t.Error("expected non-zero timestamp")
	}
}

func TestFetchARC_MissingFile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := fetchARC(ctx, filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("expected error for missing arcstats, got nil")
	}
	if !strings.Contains(err.Error(), "read arcstats") {
		t.Errorf("err = %q, want a read error", err)
	}
}

func TestFetchARC_ContextCanceled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "arcstats")
	_ = os.WriteFile(path, readARCSample(t), 0o644)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fetchARC(ctx, path)
	if err == nil {
		t.Fatal("expected error from canceled context, got nil")
	}
}

func TestHandleARCAPI_MethodNotAllowed(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/api/arc", nil)
		rec := httptest.NewRecorder()
		HandleARCAPI(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status = %d, want 405", method, rec.Code)
		}
	}
}

func TestHandleARCAPI_ServerError(t *testing.T) {
	// Override the path resolver indirectly: since resolveARCPath always
	// returns the fixed /proc path, on a host without ZFS loaded the handler
	// must return 500, not crash. If the host happens to have ZFS, the
	// endpoint returns 200 — so assert either 200 or 500, never 5xx-body-only.
	req := httptest.NewRequest(http.MethodGet, "/api/arc", nil)
	rec := httptest.NewRecorder()
	HandleARCAPI(rec, req)
	if rec.Code != http.StatusOK && rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 200 or 500", rec.Code)
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q, want application/json*",
			rec.Header().Get("Content-Type"))
	}
}
