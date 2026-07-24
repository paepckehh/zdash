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

// readSample loads testdata/sample.json, a representative capture of
// `zpool list -v --json` output with two pools (one ONLINE with nested
// mirror vdevs, one DEGRADED and full).
func readSample(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "sample.json"))
	if err != nil {
		t.Fatalf("read testdata/sample.json: %v", err)
	}
	return b
}

func TestUnmarshalZPoolOutput_Sample(t *testing.T) {
	var got ZPoolOutput
	if err := json.Unmarshal(readSample(t), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.OutputVersion.Command != "zpool list -v" {
		t.Errorf("output_version.command = %q, want %q",
			got.OutputVersion.Command, "zpool list -v")
	}
	if got.OutputVersion.VersMajor != 1 || got.OutputVersion.VersMinor != 0 {
		t.Errorf("output_version = major=%d minor=%d, want 1/0",
			got.OutputVersion.VersMajor, got.OutputVersion.VersMinor)
	}

	if len(got.Pools) != 2 {
		t.Fatalf("pools count = %d, want 2", len(got.Pools))
	}

	tank, ok := got.Pools["tank"]
	if !ok {
		t.Fatal("missing pool \"tank\"")
	}
	if tank.State != "ONLINE" {
		t.Errorf("tank.state = %q, want ONLINE", tank.State)
	}
	if tank.Properties.Size.Value != "9.94G" {
		t.Errorf("tank size = %q, want 9.94G", tank.Properties.Size.Value)
	}
	if tank.Properties.Capacity.Value != "32%" {
		t.Errorf("tank capacity = %q, want 32%%", tank.Properties.Capacity.Value)
	}

	// Vdevs are recursive: tank -> mirror-0 -> {/dev/sda1, /dev/sdb1}.
	mirror, ok := tank.Vdevs["mirror-0"]
	if !ok {
		t.Fatal("missing vdev \"mirror-0\" under tank")
	}
	if mirror.VDevType != "mirror" {
		t.Errorf("mirror-0.vdev_type = %q, want mirror", mirror.VDevType)
	}
	if len(mirror.Vdevs) != 2 {
		t.Fatalf("mirror-0 child vdev count = %d, want 2", len(mirror.Vdevs))
	}
	sdb, ok := mirror.Vdevs["/dev/sdb1"]
	if !ok {
		t.Fatal("missing child vdev /dev/sdb1 under mirror-0")
	}
	if sdb.State != "DEGRADED" {
		t.Errorf("/dev/sdb1 state = %q, want DEGRADED", sdb.State)
	}
	if sdb.Properties.Health.Value != "DEGRADED" {
		t.Errorf("/dev/sdb1 health prop = %q, want DEGRADED",
			sdb.Properties.Health.Value)
	}
}

func TestUnmarshalZPoolOutput_Empty(t *testing.T) {
	// `zpool list -v --json` on a system with no pools returns an empty
	// pools map; the decoder must not choke on it.
	in := []byte(`{"output_version":{"command":"zpool list","vers_major":1,"vers_minor":0},"pools":{}}`)
	var got ZPoolOutput
	if err := json.Unmarshal(in, &got); err != nil {
		t.Fatalf("unmarshal empty: %v", err)
	}
	if len(got.Pools) != 0 {
		t.Errorf("expected 0 pools, got %d", len(got.Pools))
	}
}

func TestUnmarshalZPoolOutput_BadJSON(t *testing.T) {
	var got ZPoolOutput
	if err := json.Unmarshal([]byte("{not json"), &got); err == nil {
		t.Fatal("expected unmarshal error for malformed json, got nil")
	}
}

func TestUnmarshalZPoolOutput_RoundTrip(t *testing.T) {
	// Decoding then re-encoding must not drop fields the dashboard reads.
	var first ZPoolOutput
	if err := json.Unmarshal(readSample(t), &first); err != nil {
		t.Fatalf("first unmarshal: %v", err)
	}
	reencoded, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var second ZPoolOutput
	if err := json.Unmarshal(reencoded, &second); err != nil {
		t.Fatalf("second unmarshal: %v", err)
	}
	if len(second.Pools) != len(first.Pools) {
		t.Errorf("pool count changed across round-trip: %d -> %d",
			len(first.Pools), len(second.Pools))
	}
}

func TestHandleIndex(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	HandleIndex(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html*", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Pool &amp; ARC Dashboard") && !strings.Contains(body, "Pool & ARC Dashboard") {
		t.Errorf("body missing title marker; got %d bytes", len(body))
	}
	if !strings.Contains(body, "/api/zpool") {
		t.Error("body does not reference /api/zpool endpoint")
	}
	if !strings.Contains(body, "/api/arc") {
		t.Error("body does not reference /api/arc endpoint")
	}
}

func TestHandleZPoolAPI_MethodNotAllowed(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/api/zpool", nil)
		rec := httptest.NewRecorder()
		HandleZPoolAPI(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status = %d, want 405", method, rec.Code)
		}
	}
}

func TestFetchZpool_Success(t *testing.T) {
	fakeBin := writeEmitter(t, string(readSample(t)))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := fetchZpool(ctx, fakeBin)
	if err != nil {
		t.Fatalf("fetchZpool: %v", err)
	}
	if len(got.Pools) != 2 {
		t.Errorf("pools = %d, want 2", len(got.Pools))
	}
	if _, ok := got.Pools["tank"]; !ok {
		t.Error("missing pool \"tank\"")
	}
	if _, ok := got.Pools["oldpool"]; !ok {
		t.Error("missing pool \"oldpool\"")
	}
}

func TestFetchZpool_ExecFailure(t *testing.T) {
	fakeBin := writeEmitter(t, "") // exits 1, prints nothing
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := fetchZpool(ctx, fakeBin)
	if err == nil {
		t.Fatal("expected error from failing zpool, got nil")
	}
	if !strings.Contains(err.Error(), "zpool exec") {
		t.Errorf("err = %q, want a zpool exec error", err)
	}
}

func TestFetchZpool_BadJSON(t *testing.T) {
	fakeBin := writeEmitter(t, "{this is not json")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := fetchZpool(ctx, fakeBin)
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), "parse zpool json") {
		t.Errorf("err = %q, want a parse error", err)
	}
}

func TestFetchZpool_ContextCanceled(t *testing.T) {
	// A fake zpool that sleeps long enough to outlive the context.
	fakeBin := writeScript(t, "sleep 10")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled before invocation
	_, err := fetchZpool(ctx, fakeBin)
	if err == nil {
		t.Fatal("expected error from canceled context, got nil")
	}
}

// writeEmitter writes a fake zpool executable that prints the given payload
// to stdout and exits 0. If payload is empty it exits 1 to simulate exec
// failure. The payload is written to a sibling file and catted to avoid
// shell-quoting issues with the JSON content.
func writeEmitter(t *testing.T, payload string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "zpool")
	var body string
	if payload == "" {
		body = "#!/bin/sh\nexit 1\n"
	} else {
		payloadFile := filepath.Join(dir, "payload.json")
		if err := os.WriteFile(payloadFile, []byte(payload), 0o644); err != nil {
			t.Fatalf("write payload file: %v", err)
		}
		body = "#!/bin/sh\ncat " + shellQuote(payloadFile) + "\n"
	}
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatalf("write emitter: %v", err)
	}
	return bin
}

// shellQuote wraps a path in single quotes, escaping any embedded single
// quotes, so it is safe to splice into a /bin/sh body.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// writeScript writes an arbitrary shell body to a fake zpool executable.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "zpool")
	contents := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(bin, []byte(contents), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return bin
}
