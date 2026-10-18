package zdash

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sync"
)

//go:embed embed/index.html
var indexHTML embed.FS

// Version is the zDash build version, injected at build time via
// -ldflags="-X 'paepcke.de/zdash.Version=...'". It defaults to "dev" when
// no version was linked in (e.g. go run / go build without ldflags).
var Version = "dev"

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

// indexPage renders the dashboard HTML once and caches the result, so
// serving the page costs a slice copy instead of an embed.FS read plus a
// bytes.Replace on every request.
var indexPage = sync.OnceValue(func() []byte {
	data, err := indexHTML.ReadFile("embed/index.html")
	if err != nil {
		return nil
	}
	return bytes.Replace(data, []byte("__ZDASH_VERSION__"), []byte(Version), 1)
})

func HandleIndex(w http.ResponseWriter, r *http.Request) {
	page := indexPage()
	if page == nil {
		http.Error(w, "Internal Server Error: missing embedded resources", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(page)
}

// zpoolResolver is the test seam over resolveZpoolBin (mirroring
// smartctlResolver in smart.go): it lets tests point produceZpool at a
// stubbed emitter without depending on a zpool install or the NixOS
// path layout.
var zpoolResolver = resolveZpoolBin

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

// HandleZPoolAPI exposes the pool state as JSON at /api/zpool. The actual
// zpool run happens in the session-scoped background refresher (see
// refresher.go); the handler only serves the cached bytes. GET only.
func HandleZPoolAPI(w http.ResponseWriter, r *http.Request) {
	serveRefresh(w, r, "zpool")
}
