package zdash

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// readSmartFixture loads a canned smartctl JSON capture from testdata/.
func readSmartFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata/%s: %v", name, err)
	}
	return b
}

func TestParseSmartAll_NVMe(t *testing.T) {
	var raw rawSmartAll
	if err := json.Unmarshal(readSmartFixture(t, "smart_nvme.json"), &raw); err != nil {
		t.Fatalf("unmarshal nvme: %v", err)
	}
	d := parseSmartAll(&raw)
	if !d.SmartPassed {
		t.Errorf("smart_passed = false, want true")
	}
	if d.ModelName != "SAMSUNG MZVL8512HDLU-00BLL" {
		t.Errorf("model = %q", d.ModelName)
	}
	if d.CapacityBytes != 512110190592 {
		t.Errorf("capacity = %d, want 512110190592", d.CapacityBytes)
	}
	if d.Protocol != "NVMe" {
		t.Errorf("protocol = %q, want NVMe", d.Protocol)
	}
	if d.TemperatureCurrent != 48 {
		t.Errorf("temp = %d, want 48", d.TemperatureCurrent)
	}
	if d.TemperatureCritical != 85 {
		t.Errorf("temp critical = %d, want 85", d.TemperatureCritical)
	}
	if d.NVMe == nil {
		t.Fatal("missing nvme health")
	}
	if d.NVMe.PercentageUsed != 0 {
		t.Errorf("percentage_used = %d, want 0", d.NVMe.PercentageUsed)
	}
	if d.NVMe.AvailableSpare != 100 {
		t.Errorf("available_spare = %d, want 100", d.NVMe.AvailableSpare)
	}
	if d.NVMe.MediaErrors != 0 {
		t.Errorf("media_errors = %d, want 0", d.NVMe.MediaErrors)
	}
	if d.NVMe.UnsafeShutdowns != 35 {
		t.Errorf("unsafe_shutdowns = %d, want 35", d.NVMe.UnsafeShutdowns)
	}
	if d.ATA != nil {
		t.Errorf("ata health should be nil for nvme device")
	}
}

func TestParseSmartAll_ATA(t *testing.T) {
	var raw rawSmartAll
	if err := json.Unmarshal(readSmartFixture(t, "smart_sda.json"), &raw); err != nil {
		t.Fatalf("unmarshal sda: %v", err)
	}
	d := parseSmartAll(&raw)
	if !d.SmartPassed {
		t.Errorf("smart_passed = false, want true")
	}
	if d.ModelFamily != "Seagate IronWolf" {
		t.Errorf("family = %q", d.ModelFamily)
	}
	if d.RotationRate != 5980 {
		t.Errorf("rotation = %d, want 5980", d.RotationRate)
	}
	if d.CapacityBytes != 4000787030016 {
		t.Errorf("capacity = %d, want 4000787030016", d.CapacityBytes)
	}
	if d.TemperatureCurrent != 28 {
		t.Errorf("temp = %d, want 28", d.TemperatureCurrent)
	}
	if d.PowerOnHours != 45488 {
		t.Errorf("power_on_hours = %d, want 45488", d.PowerOnHours)
	}
	if d.PowerCycleCount != 2916 {
		t.Errorf("power_cycle_count = %d, want 2916", d.PowerCycleCount)
	}
	if d.ATA == nil {
		t.Fatal("missing ata health")
	}
	if len(d.ATA.Attributes) < 8 {
		t.Errorf("attributes = %d, want >= 8", len(d.ATA.Attributes))
	}
	// Find the Reallocated_Sector_Ct attribute (id 5).
	var reallocated *ATAAttribute
	for i := range d.ATA.Attributes {
		if d.ATA.Attributes[i].ID == 5 {
			reallocated = &d.ATA.Attributes[i]
			break
		}
	}
	if reallocated == nil {
		t.Fatal("missing attribute id 5")
	}
	if reallocated.Value != 100 || reallocated.RawValue != 0 {
		t.Errorf("reallocated = value %d raw %d, want 100/0", reallocated.Value, reallocated.RawValue)
	}
	if !d.ATA.SelfTest.Passed {
		t.Errorf("self_test passed = false, want true")
	}
	if d.NVMe != nil {
		t.Errorf("nvme health should be nil for ata device")
	}
}

func TestParseSmartAll_Empty(t *testing.T) {
	// An empty smartctl JSON must not panic and must yield zero values.
	d := parseSmartAll(&rawSmartAll{})
	if d.SmartPassed {
		t.Error("smart_passed should default false")
	}
	if d.NVMe != nil || d.ATA != nil {
		t.Error("transport health should be nil for empty raw")
	}
}

func TestSmartctlVersion(t *testing.T) {
	if got := smartctlVersion([]int{7, 5}); got != "7.5" {
		t.Errorf("version = %q, want 7.5", got)
	}
	if got := smartctlVersion(nil); got != "" {
		t.Errorf("nil version = %q, want empty", got)
	}
	if got := smartctlVersion([]int{8}); got != "8" {
		t.Errorf("single = %q, want 8", got)
	}
}

func TestFetchSmartScan_Success(t *testing.T) {
	bin := writeSmartEmitter(t, string(readSmartFixture(t, "smart_scan.json")))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	scan, err := fetchSmartScan(ctx, bin)
	if err != nil {
		t.Fatalf("fetchSmartScan: %v", err)
	}
	if len(scan.Devices) != 4 {
		t.Errorf("devices = %d, want 4", len(scan.Devices))
	}
	if scan.Devices[3].Name != "/dev/nvme0" {
		t.Errorf("device 3 name = %q, want /dev/nvme0", scan.Devices[3].Name)
	}
}

func TestFetchSmartScan_BadJSON(t *testing.T) {
	bin := writeSmartEmitter(t, "{not json")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := fetchSmartScan(ctx, bin)
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), "parse smartctl scan json") {
		t.Errorf("err = %q, want a parse error", err)
	}
}

func TestFetchSmartScan_ContextCanceled(t *testing.T) {
	bin := writeSmartScript(t, "sleep 10")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fetchSmartScan(ctx, bin)
	if err == nil {
		t.Fatal("expected error from canceled context, got nil")
	}
}

func TestFetchSmartDevice_Success(t *testing.T) {
	bin := writeSmartDeviceBin(t, string(readSmartFixture(t, "smart_sda.json")))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d, err := fetchSmartDevice(ctx, bin, "/dev/sda")
	if err != nil {
		t.Fatalf("fetchSmartDevice: %v", err)
	}
	if !d.SmartPassed {
		t.Error("smart_passed = false, want true")
	}
	if d.ATA == nil {
		t.Error("missing ATA health")
	}
}

func TestFetchSmartDevice_BadJSON(t *testing.T) {
	bin := writeSmartDeviceBin(t, "{not json")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := fetchSmartDevice(ctx, bin, "/dev/sda")
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), "parse smartctl json") {
		t.Errorf("err = %q, want a parse error", err)
	}
}

func TestHandleSmartAPI_MethodNotAllowed(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/api/smart", nil)
		rec := httptest.NewRecorder()
		HandleSmartAPI(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status = %d, want 405", method, rec.Code)
		}
	}
}

func TestHandleSmartAPI_SmartctlMissing(t *testing.T) {
	if _, err := exec.LookPath("smartctl"); err == nil {
		t.Skip("smartctl present on host; missing-tool path not testable here")
	}
	if _, err := os.Stat(nixSmartctl); err == nil {
		t.Skip("smartctl present at NixOS path; missing-tool path not testable here")
	}
	req := httptest.NewRequest(http.MethodGet, "/api/smart", nil)
	t.Setenv("PATH", t.TempDir())
	rec := httptest.NewRecorder()
	HandleSmartAPI(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var report SmartReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if report.SmartctlAvailable {
		t.Error("smartctl_available = true, want false (smartctl not installed)")
	}
	if len(report.Devices) != 0 {
		t.Errorf("devices = %d, want 0", len(report.Devices))
	}
	if report.ScanError == "" {
		t.Error("scan_error should be populated when smartctl is missing")
	}
}

// withSmartctlBin swaps the package-level smartctl resolver to point at bin
// for the duration of the test, restoring it afterwards. This lets the
// handler's full pipeline (scan + per-device --xall + normalise) run against
// canned fixtures without a real smartctl install.
func withSmartctlBin(t *testing.T, bin string) {
	t.Helper()
	prev := smartctlResolver
	smartctlResolver = func() string { return bin }
	t.Cleanup(func() { smartctlResolver = prev })
}

// smartctlStub writes a fake smartctl that dispatches on its argv: for
// `--scan` it prints the scan fixture, for `--xall ... <dev>` it prints the
// fixture mapped from the device basename (sda -> smart_sda.json, nvme0 ->
// smart_nvme.json).
func smartctlStub(t *testing.T) string {
	t.Helper()
	scan := string(readSmartFixture(t, "smart_scan.json"))
	nvme := string(readSmartFixture(t, "smart_nvme.json"))
	sda := string(readSmartFixture(t, "smart_sda.json"))
	dir := t.TempDir()
	bin := filepath.Join(dir, "smartctl")
	body := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--scan\" ]; then\n" +
		"cat <<'EOF_SCAN'\n" + scan + "\nEOF_SCAN\n" +
		"exit 0\n" +
		"fi\n" +
		"dev=\"$3\"\n" +
		"base=\"$(basename \"$dev\")\"\n" +
		"case \"$base\" in\n" +
		"nvme0) cat <<'EOF_D'\n" + nvme + "\nEOF_D\n;;\n" +
		"sda) cat <<'EOF_D'\n" + sda + "\nEOF_D\n;;\n" +
		"*) echo '{}'\n;;\n" +
		"esac\n" +
		"exit 0\n"
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return bin
}

func TestHandleSmartAPI_FullPipeline(t *testing.T) {
	withSmartctlBin(t, smartctlStub(t))
	req := httptest.NewRequest(http.MethodGet, "/api/smart", nil)
	rec := httptest.NewRecorder()
	HandleSmartAPI(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var report SmartReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !report.SmartctlAvailable {
		t.Error("smartctl_available = false, want true")
	}
	if report.SmartctlVersion != "7.5" {
		t.Errorf("version = %q, want 7.5", report.SmartctlVersion)
	}
	// scan fixture declares sda, sdb, sdc, nvme0 -> 4 devices.
	if len(report.Devices) != 4 {
		t.Fatalf("devices = %d, want 4", len(report.Devices))
	}
	byName := make(map[string]SmartDeviceReport, len(report.Devices))
	for _, d := range report.Devices {
		byName[d.Name] = d
	}
	nvme, ok := byName["/dev/nvme0"]
	if !ok {
		t.Fatal("missing /dev/nvme0")
	}
	if !nvme.OK || nvme.Details == nil {
		t.Fatalf("nvme probe failed: %v", nvme.Error)
	}
	if !nvme.Details.SmartPassed {
		t.Error("nvme smart_passed = false, want true")
	}
	if nvme.Details.NVMe == nil {
		t.Error("nvme details missing NVMe health")
	}
	if nvme.Details.NVMe.PercentageUsed != 0 {
		t.Errorf("nvme percentage_used = %d, want 0", nvme.Details.NVMe.PercentageUsed)
	}
	sda, ok := byName["/dev/sda"]
	if !ok {
		t.Fatal("missing /dev/sda")
	}
	if !sda.OK || sda.Details == nil {
		t.Fatalf("sda probe failed: %v", sda.Error)
	}
	if sda.Details.ATA == nil {
		t.Error("sda details missing ATA health")
	}
	if len(sda.Details.ATA.Attributes) < 8 {
		t.Errorf("sda attributes = %d, want >= 8", len(sda.Details.ATA.Attributes))
	}
	// sdb/sdc hit the wildcard '{}' branch and must report ok with empty details.
	sdb, ok := byName["/dev/sdb"]
	if !ok {
		t.Fatal("missing /dev/sdb")
	}
	if !sdb.OK {
		t.Errorf("sdb ok = false, want true (wildcard fixture): %v", sdb.Error)
	}
}

// writeSmartEmitter writes a fake smartctl that prints the given payload to
// stdout and exits 0, mirroring writeEmitter used for the zpool tests.
func writeSmartEmitter(t *testing.T, payload string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "smartctl")
	if err := os.WriteFile(bin, smartShBody(payload), 0o755); err != nil {
		t.Fatalf("write emitter: %v", err)
	}
	return bin
}

// writeSmartDeviceBin writes a fake smartctl that ignores its argv and prints
// the given payload (used for fetchSmartDevice, which passes --xall --json
// <dev>).
func writeSmartDeviceBin(t *testing.T, payload string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "smartctl")
	if err := os.WriteFile(bin, smartShBody(payload), 0o755); err != nil {
		t.Fatalf("write device bin: %v", err)
	}
	return bin
}

// writeSmartScript writes an arbitrary shell body as a fake smartctl.
func writeSmartScript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "smartctl")
	contents := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(bin, []byte(contents), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return bin
}

// smartShBody emits a /bin/sh body that prints the payload (or exits 1 when
// empty) while ignoring any argv smartctl is called with.
func smartShBody(payload string) []byte {
	if payload == "" {
		return []byte("#!/bin/sh\nexit 1\n")
	}
	// Encode the payload as a base64-free heredoc so shell-quoting in the
	// JSON cannot break the script body.
	return []byte("#!/bin/sh\ncat <<" + "EOF_SMART_FIXTURE" + "\n" + payload + "\nEOF_SMART_FIXTURE\n")
}
