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

// Linux /proc and /sys paths consulted by the system-info scraper. They are
// plain constants (not configurable via env) because the proc/sys layout is
// fixed by the kernel and is unaffected by the NixOS per-profile binary
// layout.
const (
	procOSRelease  = "/etc/os-release"
	procUptime     = "/proc/uptime"
	procMemInfo    = "/proc/meminfo"
	procCPUInfo    = "/proc/cpuinfo"
	procLoadAvg    = "/proc/loadavg"
	procStat       = "/proc/stat"
	procNetRoute   = "/proc/net/route"
	procNetIPv4    = "/proc/net/fib_trie"
	procRoot       = "/proc"
	cpuFreqBase    = "/sys/devices/system/cpu/cpu0/cpufreq/scaling_min_freq"
	cpuFreqMax     = "/sys/devices/system/cpu/cpu0/cpufreq/scaling_max_freq"
	cpuFreqCur     = "/sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq"
	dmiSysVendor   = "/sys/class/dmi/id/sys_vendor"
	dmiProductName = "/sys/class/dmi/id/product_name"
	dmiProductVer  = "/sys/class/dmi/id/product_version"
	dmiBoardName   = "/sys/class/dmi/id/board_name"
	// zfsDev is the Linux ZFS control device. Its presence indicates that
	// the zfs kernel module is loaded and that the zpool subprocess can
	// talk to it. The Pools tab is suppressed when it is absent.
	zfsDev = "/dev/zfs"
)

// SysInfo holds a fastfetch-style snapshot of the host running zdash. Most
// fields come from /proc on Linux; on OSes without /proc the corresponding
// fields are zero-valued so the frontend can degrade gracefully. Sizes are in
// bytes; the frontend formats them.
type SysInfo struct {
	Hostname string `json:"hostname"`
	// FQDN is the fully-qualified domain name read from
	// /proc/sys/kernel/domainname ("none" on hosts without a domain). It
	// lets the dashboard show host.example.com alongside the short hostname.
	FQDN      string `json:"fqdn"`
	OS        string `json:"os"`
	OSID      string `json:"os_id"`
	OSVersion string `json:"os_version"`
	// OSCodename, OSVariant and OSBuildID carry extra /etc/os-release keys
	// (e.g. NixOS "zokor", Debian "bookworm") that the pretty-name omits.
	OSCodename    string `json:"os_codename"`
	OSVariant     string `json:"os_variant"`
	OSBuildID     string `json:"os_build_id"`
	Kernel        string `json:"kernel"`
	KernelRelease string `json:"kernel_release"`
	KernelVersion string `json:"kernel_version"`
	Machine       string `json:"machine"`
	// Host vendor / product / version come from /sys/class/dmi/id/* (SMBIOS).
	// On hosts without DMI (containers, some ARM boards) the files are absent
	// and the fields stay empty so the frontend can hide the row.
	HostVendor    string  `json:"host_vendor"`
	HostProduct   string  `json:"host_product"`
	HostVersion   string  `json:"host_version"`
	HostFamily    string  `json:"host_family"`
	UptimeSeconds float64 `json:"uptime_seconds"`
	Uptime        string  `json:"uptime"`
	BootTime      int64   `json:"boot_time"`
	CPUModel      string  `json:"cpu_model"`
	// CPUVendor is the x86 vendor_id string ("AuthenticAMD", "GenuineIntel").
	// Empty on non-x86 or when /proc/cpuinfo omits the line.
	CPUVendor string `json:"cpu_vendor"`
	CPUCores  int    `json:"cpu_cores"`
	// CPUPhysicalCores is the count of physical cores per package (the
	// "cpu cores" line in /proc/cpuinfo); CPUCores is the logical/logical
	// online count (the "processor" entries).
	CPUPhysicalCores int `json:"cpu_physical_cores"`
	// CPUFreqMHz is the current operating frequency of cpu0, read from
	// /proc/cpuinfo "cpu MHz" (or the cpufreq sysfs file as a fallback).
	// CPUBaseMHz and CPUMaxMHz are the scaling_min/max limits from cpufreq
	// sysfs; they are zero when the cpufreq driver is absent (bare metal
	// without DVFS, or kernels that hide the files).
	CPUFreqMHz           float64 `json:"cpu_freq_mhz"`
	CPUBaseMHz           float64 `json:"cpu_base_mhz"`
	CPUMaxMHz            float64 `json:"cpu_max_mhz"`
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
	Locale               string  `json:"locale"`
	Processes            int     `json:"processes"`
	// NetIface is the primary outbound interface (the default route's device,
	// from /proc/net/route); NetIPv4 is its primary IPv4 address in CIDR
	// form. Both are empty when there is no default route (e.g. a host
	// without a configured NIC).
	NetIface string `json:"net_iface"`
	NetIPv4  string `json:"net_ipv4"`
	// ZFSAvailable reports whether a usable ZFS control device (/dev/zfs on
	// Linux) is present. The Pools tab can only produce data when this is
	// true; the frontend suppresses the tab otherwise.
	ZFSAvailable bool `json:"zfs_available"`
	// ARCAvailable reports whether the ZFS ARC kstat is exposed under /proc.
	// The ARC Cache tab is suppressed when false.
	ARCAvailable bool `json:"arc_available"`
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
	if b, err := os.ReadFile("/proc/sys/kernel/domainname"); err == nil {
		if d := strings.TrimSpace(string(b)); d != "" && d != "(none)" && d != "none" {
			s.FQDN = d
		}
	}

	if b, err := os.ReadFile(resolveSysInfoPath(procOSRelease)); err == nil {
		id, pretty, ver, codename, variant, buildID := parseOSRelease(b)
		s.OSID = id
		s.OS = pretty
		s.OSVersion = ver
		s.OSCodename = codename
		s.OSVariant = variant
		s.OSBuildID = buildID
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
		s.CPUModel, s.CPUVendor, s.CPUCores, s.CPUPhysicalCores, s.CPUFreqMHz = parseCPUInfo(b)
	} else {
		log.Printf("⚠️  sysinfo: %s: %v", procCPUInfo, err)
	}
	if s.CPUCores == 0 {
		s.CPUCores = runtime.NumCPU()
	}

	// cpufreq sysfs gives the min/max/cur limits that /proc/cpuinfo omits on
	// scaling-capable CPUs. Files are absent on bare metal without DVFS or
	// in containers that hide sysfs, in which case the limits stay zero and
	// only CPUFreqMHz (from /proc/cpuinfo) is reported.
	s.CPUBaseMHz, s.CPUMaxMHz = readCPUFreqLimits()
	if s.CPUFreqMHz == 0 {
		if cur, err := os.ReadFile(cpuFreqCur); err == nil {
			s.CPUFreqMHz = khzToMHz(string(cur))
		}
	}

	// Host SMBIOS strings. Read each file individually because some boards
	// expose only a subset (e.g. product_version is frequently blank).
	s.HostVendor = readTrim(dmiSysVendor)
	s.HostProduct = readTrim(dmiProductName)
	s.HostVersion = readTrim(dmiProductVer)
	s.HostFamily = readTrim(dmiBoardName)

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
	// Locale is the active internationalization setting. Prefer $LC_ALL,
	// then $LC_MESSAGES, then $LANG (the POSIX fallback chain). Empty on a
	// minimal "C" environment that sets none of them.
	s.Locale = firstNonEmpty(os.Getenv("LC_ALL"), os.Getenv("LC_MESSAGES"), os.Getenv("LANG"))
	s.Processes = countProcesses(procRoot)

	// Default-route interface + its primary IPv4. Read from /proc/net/route
	// (the kernel's view of the routing table) and /proc/net/fib_trie (the
	// FIB, which lists interface addresses). Both files are absent or empty
	// on hosts without IPv4, leaving the fields blank so the frontend can
	// hide the row gracefully.
	if b, err := os.ReadFile(resolveSysInfoPath(procNetRoute)); err == nil {
		if iface := parseDefaultRoute(b); iface != "" {
			s.NetIface = iface
			if fb, ferr := os.ReadFile(resolveSysInfoPath(procNetIPv4)); ferr == nil {
				s.NetIPv4 = parseIPv4ForIface(fb, iface)
			}
		}
	} else {
		log.Printf("⚠️  sysinfo: %s: %v", procNetRoute, err)
	}

	// Capability probes: the Pools and ARC Cache tabs are suppressed on
	// hosts that lack a ZFS kernel. /dev/zfs is the Linux ZFS control
	// device required by the zpool subprocess; the arcstats kstat is the
	// /proc entry consumed by the ARC scraper. Both are cheap stat calls.
	if _, err := os.Stat(zfsDev); err == nil {
		s.ZFSAvailable = true
	}
	if _, err := os.Stat(arcstatsPath); err == nil {
		s.ARCAvailable = true
	}

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

// parseOSRelease extracts ID, PRETTY_NAME, VERSION, VERSION_CODENAME, VARIANT,
// and BUILD_ID from an /etc/os-release blob. Lines without `=` or with an
// empty value are skipped. Quoted values have their surrounding quotes
// stripped. Unknown keys are ignored so distro-specific additions keep the
// parser working.
func parseOSRelease(raw []byte) (id, pretty, version, codename, variant, buildID string) {
	for line := range strings.SplitSeq(string(raw), "\n") {
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
		case "VERSION_CODENAME":
			codename = val
		case "VARIANT":
			variant = val
		case "VARIANT_ID":
			if variant == "" {
				variant = val
			}
		case "BUILD_ID":
			buildID = val
		}
	}
	return id, pretty, version, codename, variant, buildID
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
	for line := range strings.SplitSeq(string(raw), "\n") {
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

// parseCPUInfo returns the first `model name`, the `vendor_id`, the count of
// `processor` entries (logical cores), the `cpu cores` count per package
// (physical cores) and the first `cpu MHz` reading from /proc/cpuinfo.
func parseCPUInfo(raw []byte) (model, vendor string, logical, physical int, freqMHz float64) {
	for line := range strings.SplitSeq(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if model == "" {
			if v, ok := strings.CutPrefix(line, "model name"); ok {
				_, val, ok := strings.Cut(v, ":")
				if ok {
					model = strings.TrimSpace(val)
				}
			}
		}
		if vendor == "" {
			if v, ok := strings.CutPrefix(line, "vendor_id"); ok {
				_, val, ok := strings.Cut(v, ":")
				if ok {
					vendor = strings.TrimSpace(val)
				}
			}
		}
		if physical == 0 {
			if v, ok := strings.CutPrefix(line, "cpu cores"); ok {
				_, val, ok := strings.Cut(v, ":")
				if ok {
					if n, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
						physical = n
					}
				}
			}
		}
		if freqMHz == 0 {
			if v, ok := strings.CutPrefix(line, "cpu MHz"); ok {
				_, val, ok := strings.Cut(v, ":")
				if ok {
					if f, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err == nil {
						freqMHz = f
					}
				}
			}
		}
		if strings.HasPrefix(line, "processor") {
			logical++
		}
	}
	return model, vendor, logical, physical, freqMHz
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
	for line := range strings.SplitSeq(string(raw), "\n") {
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

// readTrim reads a file and returns its trimmed contents, or "" on any read
// error. Used for /sys/class/dmi/id/* where individual files are frequently
// absent on containers or ARM boards.
func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// readCPUFreqLimits returns the min/max operating frequency of cpu0 from the
// cpufreq sysfs files, expressed in MHz. Returns 0, 0 when the cpufreq driver
// is absent (bare metal without DVFS, or kernels that hide the files).
func readCPUFreqLimits() (min, max float64) {
	if b, err := os.ReadFile(cpuFreqBase); err == nil {
		min = khzToMHz(string(b))
	}
	if b, err := os.ReadFile(cpuFreqMax); err == nil {
		max = khzToMHz(string(b))
	}
	return
}

// khzToMHz converts a cpufreq sysfs value (kHz, integer ASCII) to MHz. A
// parse error or empty input yields 0.
func khzToMHz(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return v / 1000
}

// parseDefaultRoute scans /proc/net/route and returns the interface name of
// the default route (the row whose destination is 00000000). The hex gateway
// and flags columns are ignored; only Iface is returned. Returns "" when no
// default route exists.
func parseDefaultRoute(raw []byte) string {
	lines := strings.Split(string(raw), "\n")
	if len(lines) < 2 {
		return ""
	}
	// Header: Iface Destination Gateway Flags ...
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		if strings.EqualFold(fields[1], "00000000") {
			return fields[0]
		}
	}
	return ""
}

// parseIPv4ForIface scans /proc/net/fib_trie for the first local, non-link
// IPv4 address owned by iface. /proc/net/fib_trie does not name interfaces,
// so this is a heuristic: it returns the first /32 host-LOCAL address that is
// not 127.x and not 0.0.0.0. The file lists each address on a `|-- <ip>`
// line followed by a `/32 host LOCAL` line, so the last seen `|--` entry is
// remembered and emitted when the LOCAL marker appears. Returns "" when no
// usable address is found.
func parseIPv4ForIface(raw []byte, _ string) string {
	lastIP := ""
	for line := range strings.SplitSeq(string(raw), "\n") {
		// Address lines look like "     |-- 10.20.0.99". Track the most
		// recent one so the LOCAL marker on the following line resolves it.
		if _, after, ok := strings.Cut(line, "|--"); ok {
			lastIP = strings.TrimSpace(after)
			continue
		}
		if !strings.Contains(line, "host LOCAL") {
			continue
		}
		if lastIP == "" || lastIP == "127.0.0.1" || lastIP == "0.0.0.0" ||
			strings.HasPrefix(lastIP, "127.") {
			continue
		}
		return lastIP
	}
	return ""
}

// firstNonEmpty returns the first non-empty string in the list, or "" when
// all are empty.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
