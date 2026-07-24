package zdash

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// arcstatsPath is the canonical location of the ZFS ARC kstat on Linux.
const arcstatsPath = "/proc/spl/kstat/zfs/arcstats"

// ARCMetrics holds the most important ZFS ARC cache usage and performance
// counters, parsed from /proc/spl/kstat/zfs/arcstats. All sizes are in bytes;
// hit/miss counters are cumulative since module load. The frontend derives
// ratios and rates from these raw values so the JSON stays stable across
// kernel versions that add or rename keys.
type ARCMetrics struct {
	Hits                 uint64 `json:"hits"`
	Iohits               uint64 `json:"iohits"`
	Misses               uint64 `json:"misses"`
	MRUHits              uint64 `json:"mru_hits"`
	MRUGhostHits         uint64 `json:"mru_ghost_hits"`
	MFUHits              uint64 `json:"mfu_hits"`
	MFUGhostHits         uint64 `json:"mfu_ghost_hits"`
	DemandDataHits       uint64 `json:"demand_data_hits"`
	DemandDataMisses     uint64 `json:"demand_data_misses"`
	DemandMetadataHits   uint64 `json:"demand_metadata_hits"`
	DemandMetadataMisses uint64 `json:"demand_metadata_misses"`
	PrefetchDataHits     uint64 `json:"prefetch_data_hits"`
	PrefetchDataMisses   uint64 `json:"prefetch_data_misses"`
	PrefetchMetaHits     uint64 `json:"prefetch_metadata_hits"`
	PrefetchMetaMisses   uint64 `json:"prefetch_metadata_misses"`
	Size                 uint64 `json:"size"`
	TargetSize           uint64 `json:"c"`
	MinSize              uint64 `json:"c_min"`
	MaxSize              uint64 `json:"c_max"`
	DataSize             uint64 `json:"data_size"`
	MetadataSize         uint64 `json:"metadata_size"`
	DbufSize             uint64 `json:"dbuf_size"`
	DnodeSize            uint64 `json:"arc_dnode_size"`
	HdrSize              uint64 `json:"hdr_size"`
	L2Size               uint64 `json:"l2_size"`
	L2Asize              uint64 `json:"l2_asize"`
	L2Ndev               uint64 `json:"l2_ndev"`
	L2Hits               uint64 `json:"l2_hits"`
	L2Misses             uint64 `json:"l2_misses"`
	MemoryAllBytes       uint64 `json:"memory_all_bytes"`
	MemoryFreeBytes      uint64 `json:"memory_free_bytes"`
	MemoryAvailableBytes uint64 `json:"memory_available_bytes"`
	// Timestamp marks when the sample was read, in milliseconds since epoch.
	Timestamp int64 `json:"timestamp_ms"`
}

// resolveARCPath returns the arcstats kstat path. On a standard Linux/ZFS
// install the file always lives under /proc, so there is no $PATH lookup and
// no NixOS special-case (the proc entry is not affected by the NixOS
// per-profile binary layout).
func resolveARCPath() string { return arcstatsPath }

// fetchARC reads the arcstats kstat file and decodes the named UINT64 rows
// into ARCMetrics. It mirrors fetchZpool: a single seam between HTTP transport
// and the data source, testable with a stubbed path.
func fetchARC(ctx context.Context, path string) (*ARCMetrics, error) {
	// Honor context cancellation/timeout even though os.ReadFile is fast;
	// a stalled NFS-backed /proc mount should not hang the dashboard.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("arc context: %w", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read arcstats %s: %w", path, err)
	}

	m, err := parseARCStats(raw)
	if err != nil {
		return nil, err
	}
	m.Timestamp = time.Now().UnixMilli()
	return m, nil
}

// parseARCStats decodes the kstat text format. Layout:
//
//	line 1: header (count type flags addr update_time)
//	line 2: column names ("name type data")
//	lines 3+: "<name> <type-int> <value>"
//
// Rows whose middle field is not a small integer (metadata rows emitted by
// some kernels) are skipped. Unknown numeric rows are ignored so the parser
// keeps working when newer kernels add keys.
func parseARCStats(raw []byte) (*ARCMetrics, error) {
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 2 {
		return nil, fmt.Errorf("arcstats: truncated file (%d lines)", len(lines))
	}

	kv := make(map[string]uint64, len(lines)-2)
	for _, line := range lines[2:] {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		// Middle field is the kstat data type (4 = KSTAT_DATA_UINT64).
		if _, err := strconv.Atoi(fields[1]); err != nil {
			continue
		}
		v, err := strconv.ParseUint(fields[2], 10, 64)
		if err != nil {
			continue
		}
		kv[fields[0]] = v
	}

	m := &ARCMetrics{
		Hits:                 kv["hits"],
		Iohits:               kv["iohits"],
		Misses:               kv["misses"],
		MRUHits:              kv["mru_hits"],
		MRUGhostHits:         kv["mru_ghost_hits"],
		MFUHits:              kv["mfu_hits"],
		MFUGhostHits:         kv["mfu_ghost_hits"],
		DemandDataHits:       kv["demand_data_hits"],
		DemandDataMisses:     kv["demand_data_misses"],
		DemandMetadataHits:   kv["demand_metadata_hits"],
		DemandMetadataMisses: kv["demand_metadata_misses"],
		PrefetchDataHits:     kv["prefetch_data_hits"],
		PrefetchDataMisses:   kv["prefetch_data_misses"],
		PrefetchMetaHits:     kv["prefetch_metadata_hits"],
		PrefetchMetaMisses:   kv["prefetch_metadata_misses"],
		Size:                 kv["size"],
		TargetSize:           kv["c"],
		MinSize:              kv["c_min"],
		MaxSize:              kv["c_max"],
		DataSize:             kv["data_size"],
		MetadataSize:         kv["metadata_size"],
		DbufSize:             kv["dbuf_size"],
		DnodeSize:            kv["arc_dnode_size"],
		HdrSize:              kv["hdr_size"],
		L2Size:               kv["l2_size"],
		L2Asize:              kv["l2_asize"],
		L2Ndev:               kv["l2_ndev"],
		L2Hits:               kv["l2_hits"],
		L2Misses:             kv["l2_misses"],
		MemoryAllBytes:       kv["memory_all_bytes"],
		MemoryFreeBytes:      kv["memory_free_bytes"],
		MemoryAvailableBytes: kv["memory_available_bytes"],
	}
	return m, nil
}

// HandleARCAPI exposes ARC cache metrics as JSON at /api/arc. GET only.
func HandleARCAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	data, err := fetchARC(ctx, resolveARCPath())
	if err != nil {
		log.Printf("⚠️  arc fetch failed: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Failed to read ARC stats. Ensure ZFS is loaded and /proc/spl/kstat/zfs/arcstats is readable (usually root or zfs group).",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(data)
}
