package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/exec"
	"time"
)

// replace at link time with real version tag
// example: go build -ldflags="-X main.version=$(git describe --tags --abbrev=0 2>/dev/null || echo 'dev')"
var version = "0.1.0-dev"


//go:embed embed/index.html
var indexHTML embed.FS

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

func main() {
	var showVersion bool
	flag.BoolVar(&showVersion, "version", false, "print version")
	flag.BoolVar(&showVersion, "V", false, "print version (shorthand)")
	flag.Parse()

	if showVersion {
		log.Printf("ZDASH Version: %s", version)
		return
	}

	listen := os.Getenv("ZDASH_LISTEN")
	if listen == "" {
		listen = "127.0.0.1:8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/api/zpool", handleZPoolAPI)

	log.Printf("Starting ZFS Dashboard v%s on %s", version, listen)
	log.Fatal(http.ListenAndServe(listen, mux))
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	data, err := indexHTML.ReadFile("embed/index.html")
	if err != nil {
		http.Error(w, "Internal Server Error: missing embedded resources", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func handleZPoolAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Execute zpool with a 5s timeout to prevent hanging
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "zpool", "list", "-v", "--json")
	out, err := cmd.Output()
	if err != nil {
		log.Printf("⚠️  zpool command failed: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Failed to execute zpool command. Ensure it's installed and you have sufficient permissions (usually root or zfs group).",
		})
		return
	}

	var data ZPoolOutput
	if err := json.Unmarshal(out, &data); err != nil {
		log.Printf("⚠️  Failed to parse zpool JSON: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to parse zpool JSON output."})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(data)
}
