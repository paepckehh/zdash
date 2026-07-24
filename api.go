package zdash

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"time"
)

//go:embed embed/index.html
var indexHTML embed.FS

// zpool binary on nixos
const nixpath = "/run/current-system/sw/bin/zpool"

// ZPoolOutput represents the JSON structure from `zpool list -v --json`
type ZPoolOutput struct {
	OutputVersion struct {
		Command   string `json:"command"`
		VersMajor int    `json:"vers_major"`
		VersMinor int    `json:"vers_minor"`
	} `json:"output_version"`
	Pools map[string]Pool `json:"pools"`
}

type Pool struct {
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	State      string          `json:"state"`
	PoolGUID   string          `json:"pool_guid"`
	TXG        string          `json:"txg"`
	SPAVersion string          `json:"spa_version"`
	ZPLVersion string          `json:"zpl_version"`
	Properties PoolProperties  `json:"properties"`
	Vdevs      map[string]VDev `json:"vdevs"`
}

type PoolProperties struct {
	Size          Prop `json:"size"`
	Allocated     Prop `json:"allocated"`
	Free          Prop `json:"free"`
	Checkpoint    Prop `json:"checkpoint"`
	ExpandSize    Prop `json:"expandsize"`
	Fragmentation Prop `json:"fragmentation"`
	Capacity      Prop `json:"capacity"`
	DedupRatio    Prop `json:"dedupratio"`
	Health        Prop `json:"health"`
	AltRoot       Prop `json:"altroot"`
}

type Prop struct {
	Value  string            `json:"value"`
	Source map[string]string `json:"source"`
}

type VDev struct {
	Name       string          `json:"name"`
	VDevType   string          `json:"vdev_type"`
	GUID       string          `json:"guid"`
	Class      string          `json:"class"`
	State      string          `json:"state"`
	Path       string          `json:"path"`
	Properties PoolProperties  `json:"properties"`
	Vdevs      map[string]VDev `json:"vdevs"`
}

func HandleIndex(w http.ResponseWriter, r *http.Request) {
	data, err := indexHTML.ReadFile("embed/index.html")
	if err != nil {
		http.Error(w, "Internal Server Error: missing embedded resources", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// resolveZpoolBin picks the zpool binary to invoke. It prefers the NixOS
// layout (where zpool is not on the default $PATH) and falls back to the
// basename lookup, which relies on the caller's $PATH.
func resolveZpoolBin() string {
	if _, err := os.Stat(nixpath); err == nil {
		return nixpath
	}
	return "zpool"
}

// fetchZpool runs `zpool list -v --json` and decodes the result. It is the
// single seam between HTTP transport and the zpool subprocess, which makes it
// testable with a stubbed bin (e.g. a shell script that prints canned JSON).
func fetchZpool(ctx context.Context, bin string) (*ZPoolOutput, error) {
	cmd := exec.CommandContext(ctx, bin, "list", "-v", "--json")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("zpool exec: %w", err)
	}
	var data ZPoolOutput
	if err := json.Unmarshal(out, &data); err != nil {
		return nil, fmt.Errorf("parse zpool json: %w", err)
	}
	return &data, nil
}

func HandleZPoolAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	data, err := fetchZpool(ctx, resolveZpoolBin())
	if err != nil {
		log.Printf("⚠️  zpool fetch failed: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Failed to execute zpool command. Ensure it's installed and you have sufficient permissions (usually root or zfs group).",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(data)
}
