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
)

func readSysFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata/%s: %v", name, err)
	}
	return b
}

func TestParseOSRelease_Sample(t *testing.T) {
	id, pretty, ver, codename, variant, buildID := parseOSRelease(readSysFixture(t, "os-release"))
	if id != "nixos" {
		t.Errorf("id = %q, want nixos", id)
	}
	if pretty != "NixOS 23.11.1234 (Tapir)" {
		t.Errorf("pretty = %q, want NixOS 23.11.1234 (Tapir)", pretty)
	}
	if ver != "23.11 (Tapir)" {
		t.Errorf("version = %q, want 23.11 (Tapir)", ver)
	}
	if codename != "tapir" {
		t.Errorf("codename = %q, want tapir", codename)
	}
	if variant != "" {
		t.Errorf("variant = %q, want empty", variant)
	}
	if buildID != "" {
		t.Errorf("buildID = %q, want empty", buildID)
	}
}

func TestParseOSRelease_Empty(t *testing.T) {
	id, pretty, ver, codename, variant, buildID := parseOSRelease([]byte(""))
	if id != "" || pretty != "" || ver != "" || codename != "" || variant != "" || buildID != "" {
		t.Errorf("expected all empty, got id=%q pretty=%q ver=%q codename=%q variant=%q buildID=%q",
			id, pretty, ver, codename, variant, buildID)
	}
}

func TestParseOSRelease_QuotedAndComment(t *testing.T) {
	in := []byte("# comment\nID=\"arch\"\nPRETTY_NAME=Arch Linux\nVARIANT_ID=server\nBUILD_ID=rolling\n")
	id, pretty, _, _, variant, buildID := parseOSRelease(in)
	if id != "arch" {
		t.Errorf("id = %q, want arch", id)
	}
	if pretty != "Arch Linux" {
		t.Errorf("pretty = %q, want Arch Linux", pretty)
	}
	if variant != "server" {
		t.Errorf("variant = %q, want server", variant)
	}
	if buildID != "rolling" {
		t.Errorf("buildID = %q, want rolling", buildID)
	}
}

func TestParseMemInfo_Sample(t *testing.T) {
	total, free, avail, cached, swapTotal, swapFree :=
		parseMemInfo(readSysFixture(t, "meminfo"))
	if total != 16384000*1024 {
		t.Errorf("total = %d, want %d", total, 16384000*1024)
	}
	if free != 256000*1024 {
		t.Errorf("free = %d, want %d", free, 256000*1024)
	}
	if avail != 8192000*1024 {
		t.Errorf("available = %d, want %d", avail, 8192000*1024)
	}
	if cached != 4096000*1024 {
		t.Errorf("cached = %d, want %d", cached, 4096000*1024)
	}
	if swapTotal != 2097152*1024 {
		t.Errorf("swapTotal = %d, want %d", swapTotal, 2097152*1024)
	}
	if swapFree != 2097152*1024 {
		t.Errorf("swapFree = %d, want %d", swapFree, 2097152*1024)
	}
}

func TestParseMemInfo_Empty(t *testing.T) {
	total, free, avail, cached, swapTotal, swapFree :=
		parseMemInfo([]byte(""))
	if total != 0 || free != 0 || avail != 0 || cached != 0 ||
		swapTotal != 0 || swapFree != 0 {
		t.Errorf("expected all zero, got total=%d free=%d avail=%d cached=%d swapT=%d swapF=%d",
			total, free, avail, cached, swapTotal, swapFree)
	}
}

func TestParseCPUInfo_Sample(t *testing.T) {
	model, vendor, logical, physical, freq := parseCPUInfo(readSysFixture(t, "cpuinfo"))
	if model != "Intel(R) Core(TM) i7-8565U CPU @ 1.80GHz" {
		t.Errorf("model = %q, want Intel(R) Core(TM) i7-8565U CPU @ 1.80GHz", model)
	}
	if vendor != "GenuineIntel" {
		t.Errorf("vendor = %q, want GenuineIntel", vendor)
	}
	if logical != 4 {
		t.Errorf("logical = %d, want 4", logical)
	}
	if physical != 4 {
		t.Errorf("physical = %d, want 4", physical)
	}
	if freq != 1992.0 {
		t.Errorf("freq = %v, want 1992.0", freq)
	}
}

func TestParseCPUInfo_Empty(t *testing.T) {
	model, vendor, logical, physical, freq := parseCPUInfo([]byte(""))
	if model != "" || vendor != "" || logical != 0 || physical != 0 || freq != 0 {
		t.Errorf("expected all empty, got model=%q vendor=%q logical=%d physical=%d freq=%v",
			model, vendor, logical, physical, freq)
	}
}

func TestParseUptime_Sample(t *testing.T) {
	got := parseUptime(readSysFixture(t, "uptime"))
	if got != 187634.20 {
		t.Errorf("uptime = %v, want 187634.20", got)
	}
}

func TestParseUptime_Empty(t *testing.T) {
	if parseUptime([]byte("")) != 0 {
		t.Error("expected 0 for empty uptime")
	}
}

func TestParseLoadAvg_Sample(t *testing.T) {
	l1, l5, l15 := parseLoadAvg(readSysFixture(t, "loadavg"))
	if l1 != 0.42 || l5 != 0.55 || l15 != 0.61 {
		t.Errorf("load = %v/%v/%v, want 0.42/0.55/0.61", l1, l5, l15)
	}
}

func TestParseLoadAvg_Empty(t *testing.T) {
	l1, l5, l15 := parseLoadAvg([]byte(""))
	if l1 != 0 || l5 != 0 || l15 != 0 {
		t.Errorf("expected zeros, got %v/%v/%v", l1, l5, l15)
	}
}

func TestParseBtime_Sample(t *testing.T) {
	b := parseBtime(readSysFixture(t, "stat"))
	if b != 1700000000 {
		t.Errorf("btime = %d, want 1700000000", b)
	}
}

func TestParseBtime_Missing(t *testing.T) {
	if parseBtime([]byte("cpu  1 2 3 4\n")) != 0 {
		t.Error("expected 0 when btime absent")
	}
}

func TestFormatUptime(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "—"},
		{0.4, "—"},
		{45, "45s"},
		{120, "2m 0s"},
		{3661, "1h 1m 1s"},
		{86400 + 3600 + 60 + 1, "1d 1h 1m 1s"},
		{2*86400 + 5*3600, "2d 5h 0m 0s"},
	}
	for _, c := range cases {
		if got := formatUptime(c.in); got != c.want {
			t.Errorf("formatUptime(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSatSub(t *testing.T) {
	if satSub(10, 3) != 7 {
		t.Error("10-3 should be 7")
	}
	if satSub(3, 10) != 0 {
		t.Error("3-10 should clamp to 0")
	}
	if satSub(5, 5) != 0 {
		t.Error("5-5 should be 0")
	}
}

func TestSysInfo_RoundTrip(t *testing.T) {
	first := &SysInfo{
		Hostname:             "host",
		FQDN:                 "host.example.com",
		OS:                   "NixOS 23.11",
		OSCodename:           "tapir",
		OSVariant:            "server",
		OSBuildID:            "20260718",
		Kernel:               "Linux",
		KernelRelease:        "6.6.44",
		Machine:              "amd64",
		HostVendor:           "HP",
		HostProduct:          "t640",
		UptimeSeconds:        187634.20,
		Uptime:               "2d 4h 7m 14s",
		CPUModel:             "Intel i7",
		CPUVendor:            "GenuineIntel",
		CPUCores:             4,
		CPUPhysicalCores:     2,
		CPUFreqMHz:           1992.0,
		CPUMaxMHz:            3300.0,
		Load1:                0.42,
		MemoryTotalBytes:     16384000 * 1024,
		MemoryAvailableBytes: 8192000 * 1024,
		Shell:                "/run/current-system/sw/bin/bash",
		Locale:               "C.UTF-8",
		Processes:            1234,
		NetIface:             "enp2s0f0",
		NetIPv4:              "10.20.0.99",
		Timestamp:            1700000000000,
	}
	reencoded, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var second SysInfo
	if err := json.Unmarshal(reencoded, &second); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if second.Hostname != first.Hostname || second.MemoryTotalBytes != first.MemoryTotalBytes ||
		second.Uptime != first.Uptime || second.CPUCores != first.CPUCores ||
		second.OSCodename != first.OSCodename || second.CPUVendor != first.CPUVendor ||
		second.NetIPv4 != first.NetIPv4 || second.Locale != first.Locale ||
		second.HostProduct != first.HostProduct || second.CPUMaxMHz != first.CPUMaxMHz ||
		second.FQDN != first.FQDN {
		t.Errorf("round-trip mismatch: %+v != %+v", second, *first)
	}
}

func TestFetchSysInfo_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fetchSysInfo(ctx)
	if err == nil {
		t.Fatal("expected error from canceled context, got nil")
	}
	if !strings.Contains(err.Error(), "sysinfo context") {
		t.Errorf("err = %q, want a sysinfo context error", err)
	}
}

func TestSysInfo_CapabilityFieldsSerialize(t *testing.T) {
	// Both booleans must round-trip through JSON so the frontend can rely on
	// their presence to decide tab visibility.
	in := &SysInfo{ZFSAvailable: true, ARCAvailable: false}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out SysInfo
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out.ZFSAvailable || out.ARCAvailable {
		t.Errorf("capability round-trip mismatch: zfs=%v arc=%v", out.ZFSAvailable, out.ARCAvailable)
	}
	if !strings.Contains(string(b), "zfs_available") || !strings.Contains(string(b), "arc_available") {
		t.Errorf("JSON missing capability keys: %s", b)
	}
}

func TestHandleSysInfoAPI_MethodNotAllowed(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/api/sysinfo", nil)
		rec := httptest.NewRecorder()
		HandleSysInfoAPI(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status = %d, want 405", method, rec.Code)
		}
	}
}

func TestHandleSysInfoAPI_GET(t *testing.T) {
	// On a real host fetchSysInfo never returns an error for GET (only a
	// canceled context does), so the handler must answer 200 with JSON.
	req := httptest.NewRequest(http.MethodGet, "/api/sysinfo", nil)
	rec := httptest.NewRecorder()
	HandleSysInfoAPI(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q, want application/json*",
			rec.Header().Get("Content-Type"))
	}
	var got SysInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if got.Timestamp == 0 {
		t.Error("expected non-zero timestamp")
	}
	if got.Machine == "" {
		t.Error("expected non-empty machine arch")
	}
}

func TestCountProcesses_NonExistent(t *testing.T) {
	if countProcesses(filepath.Join(t.TempDir(), "nope")) != 0 {
		t.Error("expected 0 processes for missing dir")
	}
}

func TestParseDefaultRoute_Sample(t *testing.T) {
	iface := parseDefaultRoute(readSysFixture(t, "route"))
	if iface != "enp2s0f0" {
		t.Errorf("iface = %q, want enp2s0f0", iface)
	}
}

func TestParseDefaultRoute_Empty(t *testing.T) {
	if parseDefaultRoute([]byte("")) != "" {
		t.Error("expected empty string for empty route file")
	}
}

func TestParseDefaultRoute_None(t *testing.T) {
	in := []byte("Iface Destination Gateway Flags RefCnt Use Metric Mask MTU Window IRTT\n" +
		"lo 0000007F 00000000 0001 0 0 0 0000007F 0 0 0\n")
	if parseDefaultRoute(in) != "" {
		t.Error("expected empty when no default route")
	}
}

func TestParseIPv4ForIface_Sample(t *testing.T) {
	ip := parseIPv4ForIface(readSysFixture(t, "fib_trie"), "enp2s0f0")
	if ip != "10.20.0.99" {
		t.Errorf("ip = %q, want 10.20.0.99", ip)
	}
}

func TestParseIPv4ForIface_SkipsLoopback(t *testing.T) {
	in := []byte("   |-- 127.0.0.1\n      /32 host LOCAL\n")
	if parseIPv4ForIface(in, "lo") != "" {
		t.Error("expected empty when only loopback present")
	}
}

func TestParseIPv4ForIface_Empty(t *testing.T) {
	if parseIPv4ForIface([]byte(""), "x") != "" {
		t.Error("expected empty for empty input")
	}
}

func TestKhzToMHz(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"3300000", 3300},
		{"1992000", 1992},
		{"0", 0},
		{"notanumber", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := khzToMHz(c.in); got != c.want {
			t.Errorf("khzToMHz(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestReadTrim_MissingFile(t *testing.T) {
	if readTrim(filepath.Join(t.TempDir(), "nope")) != "" {
		t.Error("expected empty string for missing file")
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "", "c", "d"); got != "c" {
		t.Errorf("got %q, want c", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}
