package zdash

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Linux /proc paths consulted by the system-info scraper. They are plain
// constants (not configurable via env) because the proc layout is fixed by
// the kernel and is unaffected by the NixOS per-profile binary layout.
const (
	procOSRelease = "/etc/os-release"
	procUptime    = "/proc/uptime"
	procMemInfo   = "/proc/meminfo"
	procCPUInfo   = "/proc/cpuinfo"
	procLoadAvg   = "/proc/loadavg"
	procStat      = "/proc/stat"
	procRoot      = "/proc"
)

// SysInfo holds a fastfetch-style snapshot of the host running zdash. Most
// fields come from /proc on Linux; on OSes without /proc the corresponding
// fields are zero-valued so the frontend can degrade gracefully. Sizes are in
// bytes; the frontend formats them.
type SysInfo struct {
	Hostname             string  `json:"hostname"`
	OS                   string  `json:"os"`
	OSID                 string  `json:"os_id"`
	OSVersion            string  `json:"os_version"`
	Kernel               string  `json:"kernel"`
	KernelRelease        string  `json:"kernel_release"`
	KernelVersion        string  `json:"kernel_version"`
	Machine              string  `json:"machine"`
	UptimeSeconds        float64 `json:"uptime_seconds"`
	Uptime               string  `json:"uptime"`
	BootTime             int64   `json:"boot_time"`
	CPUModel             string  `json:"cpu_model"`
	CPUCores             int     `json:"cpu_cores"`
	Load1                float64 `json:"load1"`
	Load5                float64 `json:"load5"`
	Load15               float64 `json:"load15"`
	MemoryTotalBytes     uint64  `json:"memory_total_bytes"`
	MemoryFreeBytes      uint64  `json:"memory_free_bytes"`
	MemoryAvailableBytes uint64  `json:"memory_available_bytes"`
	MemoryUsedBytes      uint64  `json:"memory_used_bytes"`
	MemoryCachedBytes    uint64  `json:"memory_cached_bytes"`
	SwapTotalBytes       uint64  `json:"swap_total_bytes"`
	SwapFreeBytes        uint64  `json:"swap_free_bytes"`
	Shell                string  `json:"shell"`
	Terminal             string  `json:"terminal"`
	Processes            int     `json:"processes"`
	// Timestamp marks when the sample was read, in milliseconds since epoch.
	Timestamp int64 `json:"timestamp_ms"`
}

// resolveSysInfoPath returns the input path unchanged. It exists only so the
// fetch seam can be exercised with stubbed paths in tests (mirroring the
// resolveARCPath / resolveZpoolBin shape).
func resolveSysInfoPath(p string) string { return p }

// fetchSysInfo gathers a host snapshot from /proc and /etc/os-release. It is
// tolerant: a missing or unparseable file leaves the relevant field zero and
// is logged, so the dashboard degrades gracefully on non-Linux hosts. Only a
// canceled context produces an error.
func fetchSysInfo(ctx context.Context) (*SysInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("sysinfo context: %w", err)
	}

	s := &SysInfo{Machine: runtime.GOARCH, Timestamp: time.Now().UnixMilli()}

	if h, err := os.Hostname(); err == nil {
		s.Hostname = h
	}

	if b, err := os.ReadFile(resolveSysInfoPath(procOSRelease)); err == nil {
		id, pretty, ver := parseOSRelease(b)
		s.OSID = id
		s.OS = pretty
		s.OSVersion = ver
	} else {
		log.Printf("⚠️  sysinfo: %s: %v", procOSRelease, err)
	}

	if b, err := os.ReadFile(resolveSysInfoPath(procUptime)); err == nil {
		s.UptimeSeconds = parseUptime(b)
		s.Uptime = formatUptime(s.UptimeSeconds)
	} else {
		log.Printf("⚠️  sysinfo: %s: %v", procUptime, err)
	}

	if b, err := os.ReadFile(resolveSysInfoPath(procMemInfo)); err == nil {
		s.MemoryTotalBytes,
			s.MemoryFreeBytes,
			s.MemoryAvailableBytes,
			s.MemoryCachedBytes,
			s.SwapTotalBytes,
			s.SwapFreeBytes = parseMemInfo(b)
		if s.MemoryAvailableBytes > 0 {
			s.MemoryUsedBytes = satSub(s.MemoryTotalBytes, s.MemoryAvailableBytes)
		} else {
			s.MemoryUsedBytes = satSub(s.MemoryTotalBytes,
				s.MemoryFreeBytes+s.MemoryCachedBytes)
		}
	} else {
		log.Printf("⚠️  sysinfo: %s: %v", procMemInfo, err)
	}

	if b, err := os.ReadFile(resolveSysInfoPath(procCPUInfo)); err == nil {
		s.CPUModel, s.CPUCores = parseCPUInfo(b)
	} else {
		log.Printf("⚠️  sysinfo: %s: %v", procCPUInfo, err)
	}
	if s.CPUCores == 0 {
		s.CPUCores = runtime.NumCPU()
	}

	if b, err := os.ReadFile(resolveSysInfoPath(procLoadAvg)); err == nil {
		s.Load1, s.Load5, s.Load15 = parseLoadAvg(b)
	} else {
		log.Printf("⚠️  sysinfo: %s: %v", procLoadAvg, err)
	}

	if b, err := os.ReadFile(resolveSysInfoPath(procStat)); err == nil {
		s.BootTime = parseBtime(b)
	} else {
		log.Printf("⚠️  sysinfo: %s: %v", procStat, err)
	}

	// Kernel strings come from /proc/sys/kernel/{ostype,osrelease,version}.
	if b, err := os.ReadFile("/proc/sys/kernel/ostype"); err == nil {
		s.Kernel = strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		s.KernelRelease = strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile("/proc/sys/kernel/version"); err == nil {
		s.KernelVersion = strings.TrimSpace(string(b))
	}

	s.Shell = os.Getenv("SHELL")
	s.Terminal = os.Getenv("TERM")
	s.Processes = countProcesses(procRoot)

	return s, nil
}

// HandleSysInfoAPI exposes a host snapshot as JSON at /api/sysinfo. GET only.
func HandleSysInfoAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	data, err := fetchSysInfo(ctx)
	if err != nil {
		log.Printf("⚠️  sysinfo fetch failed: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Failed to gather system information. The host may not expose /proc (non-Linux).",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(data)
}

// parseOSRelease extracts ID, PRETTY_NAME and VERSION from an /etc/os-release
// blob. Lines without `=` or with an empty value are skipped. Quoted values
// have their surrounding quotes stripped.
func parseOSRelease(raw []byte) (id, pretty, version string) {
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		val = strings.Trim(val, `"'`)
		switch key {
		case "ID":
			id = val
		case "PRETTY_NAME":
			pretty = val
		case "VERSION":
			version = val
		}
	}
	return id, pretty, version
}

// parseMemInfo decodes /proc/meminfo. Values are reported in kB by the kernel;
// they are scaled to bytes. Unknown keys are ignored so newer kernels that add
// lines keep the parser working.
func parseMemInfo(raw []byte) (total, free, available, cached, swapTotal, swapFree uint64) {
	scan := func(line, prefix string) (uint64, bool) {
		if !strings.HasPrefix(line, prefix) {
			return 0, false
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, false
		}
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, false
		}
		return v * 1024, true
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := scan(line, "MemTotal:"); ok {
			total = v
		} else if v, ok := scan(line, "MemFree:"); ok {
			free = v
		} else if v, ok := scan(line, "MemAvailable:"); ok {
			available = v
		} else if v, ok := scan(line, "Cached:"); ok {
			cached = v
		} else if v, ok := scan(line, "SwapTotal:"); ok {
			swapTotal = v
		} else if v, ok := scan(line, "SwapFree:"); ok {
			swapFree = v
		}
	}
	return
}

// parseCPUInfo returns the first `model name` and the count of `processor`
// entries in /proc/cpuinfo.
func parseCPUInfo(raw []byte) (model string, cores int) {
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if model == "" {
			if v, ok := strings.CutPrefix(line, "model name"); ok {
				_, val, ok := strings.Cut(v, ":")
				if ok {
					model = strings.TrimSpace(val)
				}
			}
		}
		if strings.HasPrefix(line, "processor") {
			cores++
		}
	}
	return
}

// parseUptime returns the idle-agnostic uptime in seconds from /proc/uptime.
func parseUptime(raw []byte) float64 {
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return v
}

// parseLoadAvg returns the 1/5/15-minute load averages from /proc/loadavg.
func parseLoadAvg(raw []byte) (l1, l5, l15 float64) {
	fields := strings.Fields(string(raw))
	if len(fields) < 3 {
		return
	}
	l1, _ = strconv.ParseFloat(fields[0], 64)
	l5, _ = strconv.ParseFloat(fields[1], 64)
	l15, _ = strconv.ParseFloat(fields[2], 64)
	return
}

// parseBtime extracts the boot time (epoch seconds) from /proc/stat `btime`.
func parseBtime(raw []byte) int64 {
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		v, ok := strings.CutPrefix(line, "btime ")
		if !ok {
			continue
		}
		b, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return 0
		}
		return b
	}
	return 0
}

// countProcesses counts numeric directory entries under the given path, i.e.
// the number of running PIDs visible under /proc on Linux.
func countProcesses(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(e.Name()); err == nil {
			n++
		}
	}
	return n
}

// formatUptime renders seconds into a compact "2d 3h 14m" string. Zero or
// sub-second input yields "—".
func formatUptime(sec float64) string {
	if sec < 1 {
		return "—"
	}
	d := uint64(sec) / 86400
	h := (uint64(sec) % 86400) / 3600
	m := (uint64(sec) % 3600) / 60
	s := uint64(sec) % 60
	var b strings.Builder
	if d > 0 {
		fmt.Fprintf(&b, "%dd ", d)
	}
	if h > 0 || d > 0 {
		fmt.Fprintf(&b, "%dh ", h)
	}
	if m > 0 || h > 0 || d > 0 {
		fmt.Fprintf(&b, "%dm ", m)
	}
	fmt.Fprintf(&b, "%ds", s)
	return b.String()
}

// satSub returns a-b clamped at zero so "used = total - available" never
// underflows on kernels that report slightly inconsistent counts.
func satSub(a, b uint64) uint64 {
	if b >= a {
		return 0
	}
	return a - b
}
