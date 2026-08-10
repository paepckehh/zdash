# AGENTS.md

Guidance for AI agents working in the `zdash` repository — a self-hosted ZFS pool monitoring dashboard written in pure Go.

## FIXED REQUIREMENT — EVERY CHANGE, NO EXCEPTIONS

 Before a task or change is considered done, all five steps below MUST be completed
 in this exact order. Skipping or reordering any step is a failure. Committing and
 tagging are NOT optional, are NOT gated on the user asking, and must NEVER be
 skipped, deferred, or omitted — even for a one-line fix, even for docs-only
 changes, even when the user did not say "commit". If you cannot commit/tag
 (e.g. no git identity), stop and report instead of leaving the tree uncommitted.

 1. **Format source code** — run `gofmt -w .` (or `make check`) so the tree
    stays gofmt-clean.
 2. **Build** — `go build ./cmd/zdash` must succeed (producing no binary is fine;
    `make build` is the wrapper if you want it).
 3. **Test** — `go test -count=1 ./...` must be green (add `-race` when CGO
    is available: `CGO_ENABLED=1 go test -race -count=1 ./...`).
 4. **Commit** — `git add <relevant files> && git commit -m '<message>'`.
    ALWAYS commit. The user asking "fix X" implies "commit the fix". Do not ask
    for the permission to commit, do not leave changes staged or unstaged, do not
    stop after build/test passes. Follow the commit message style of recent
    history (`git log --oneline -5`).
 5. **Tag** — bump the patch segment only and push-free create the tag locally.
    The result is `v0.<minor>.<N+1>` following the existing tag line (current
    series is `v0.1.x`, so the next tag is `v0.1.<N+1>`). Read the highest
    existing `v*` tag with `git tag --list 'v*' --sort=-v:refname | head -1`,
    bump its patch number, and run `git tag <new-tag>`. Never move, delete,
    or reuse an existing tag. Tagging is mandatory for every change, including
    docs-only and config-only changes — there is no "too small to tag" case.

## Project Overview

`zdash` is an HTTP server that runs `zpool list -v --json` per request and serves the result either as raw JSON (`/api/zpool`) or as an embedded HTML dashboard (`/`). All HTML/CSS/JS is embedded into the binary via `embed.FS`; there is no frontend build step and no third-party Go dependencies (`go.mod` has zero requires).

Module path: `paepcke.de/zdash`. Targets Linux, FreeBSD, and macOS (any OS with a `zpool` binary). The Go module line is currently `go 1.26.4`.

## Repository Layout

The tree is split into a library package at the repo root (`package zdash`) and a thin `cmd/zdash/` entrypoint (`package main`):

```
.
├── api.go            # zpool scraper + ZPoolOutput/Pool/VDev/Prop types, HandleIndex, HandleZPoolAPI
├── arc.go            # ARC kstat scraper (/proc/spl/kstat/zfs/arcstats), HandleARCAPI
├── sysinfo.go        # Host snapshot scraper (/proc + /sys), HandleSysInfoAPI
├── api_test.go       # zpool JSON parsing tests (canned fixtures in testdata/)
├── arc_test.go       # ARC kstat parsing tests (canned fixtures in testdata/)
├── sysinfo_test.go   # /proc + /sys parser tests (canned fixtures in testdata/)
├── cmd/zdash/main.go # package main — wires http.Server + routes, reads BIND_ADDR
├── embed/index.html  # single-file dashboard (HTML/CSS/JS), served via embed.FS
├── testdata/         # canned fixtures: sample.json, arcstats, fib_trie, route, ...
├── Makefile
├── .goreleaser.yml
└── .github/workflows/  # golang.yml (build/vet), release.yml (goreleaser on v* tags), codeql.yml
```

`go build ./cmd/zdash` and `go test ./...` from the repo root both succeed today.

## Build, Test, Lint Commands

The test suites (`api_test.go`, `arc_test.go`, `sysinfo_test.go`) use canned fixtures in `testdata/` (`sample.json` for zpool JSON, `arcstats` for the ARC kstat, plus `fib_trie` / `route` for the sysinfo network parser).

From the repo root:

- **Lint / format** (Makefile `check` target):
  ```bash
  make check
  ```
  Runs `go fmt`, `go vet`, `go fix` with `CGO_ENABLED=0`. The `golangci-lint` and `staticcheck` lines are commented out in the Makefile; run them manually (`golangci-lint run`, `staticcheck ./...`) if you want the fuller check.

- **Build the server binary** (Makefile `build` target, or directly):
  ```bash
  make build
  # equivalently:
  go build -C cmd/zdash -o ../../zdash -ldflags="-X main.version=$(git describe --tags --abbrev=0 2>/dev/null || echo 'dev')"
  ```

- **Run via Go toolchain** (documented in README):
  ```bash
  go run paepcke.de/zdash/cmd/zdash@latest
  ```

- **CI** (`.github/workflows/golang.yml`): `CGO_ENABLED=0 go build ./...` then `go vet ./...` on Go 1.26 across ubuntu/macos/windows. Note: Windows is in the matrix even though the `zpool` binary is effectively never present there.

- **Releases** (`.github/workflows/release.yml`): triggered by `v*` tags, runs `goreleaser release --clean` with `CGO_ENABLED=0`. See `.goreleaser.yml` for targets (linux/freebsd/darwin, amd64/arm64) and `nfpms` block for `.deb`/`.rpm` packaging. Note the goreleaser `license` field says "BSD 3-Clause" while `LICENSE` is MIT — do not "fix" this blindly without confirming maintainer intent.

- **Regenerate `go.mod`** (Makefile `deps` target): deletes and re-creates `go.mod`/`go.sum` via `go mod init paepcke.de/zdash && go mod tidy`. Only run if you intend to wipe the existing module file.

## Architecture and Data Flow

1. **Entry** (`cmd/zdash/main.go`): constructs an `http.Server` with explicit read/write/idle timeouts (10s/30s/120s) and registers `/` → `HandleIndex`, `/api/zpool` → `HandleZPoolAPI`, `/api/arc` → `HandleARCAPI`, `/api/sysinfo` → `HandleSysInfoAPI`. Bind address comes from `BIND_ADDR` (default `127.0.0.1:8080`).
2. **`/` (HandleIndex)** — reads `embed/index.html` from the embedded FS and writes it verbatim. No server-side templating; initial data is fetched by the client.
3. **`/api/zpool` (HandleZPoolAPI)** — only `GET` is allowed (else `405`). Creates a `5*time.Second` context, locates the `zpool` binary, runs `zpool list -v --json`, unmarshals into `ZPoolOutput`, and returns it as JSON. Failures at either the exec or parse stage return `500` with a small JSON error body; the server never crashes.
4. **`/api/arc` (HandleARCAPI)** — reads `/proc/spl/kstat/zfs/arcstats`, parses the kstat text format, returns `*ARCMetrics` as JSON (GET only). Same error/JSON conventions as the zpool endpoint.
5. **`/api/sysinfo` (HandleSysInfoAPI)** — reads `/proc` and `/sys` (OS release, meminfo, CPU info, loadavg, uptime, default route, IPv4, DMI/SMBIOS) and returns a `SysInfo` snapshot as JSON (GET only). On non-Linux OSes or hosts without these files, fields degrade to empty strings/zeros and the endpoint still renders what it can.
6. **`zpool` binary resolution** (`api.go`): defaults to `zpool` on `$PATH`, but if `/run/current-system/sw/bin/zpool` exists it uses that instead. This is the NixOS-specific path — **do not remove this stat-check**, it is the only thing making the dashboard work on NixOS where `zpool` is not on the default `$PATH`.
7. **Client side** (`embed/index.html`, ~1500 lines, single file): vanilla JS, no framework. Polls `/api/zpool`, `/api/arc`, and `/api/sysinfo` in parallel on a timer, renders a tabbed dashboard (Pools / ARC Cache / System) with donut charts, sparklines, gradient KPI tiles, and a user-switchable light/dark theme. History for the hit-ratio sparkline is kept in-memory across refreshes.

## ARC Scraper

`arc.go` mirrors the zpool scraper but reads `/proc/spl/kstat/zfs/arcstats` (a text kstat file) instead of spawning a subprocess:

- `parseARCStats` decodes the kstat text format: line 1 is a header, line 2 is column names (`name type data`), lines 3+ are `<key> <type-int> <value>` rows. The `type` field is a small integer (4 = `KSTAT_DATA_UINT64`); rows whose middle field is not an integer are skipped. Unknown numeric keys are ignored so the parser keeps working when newer kernels add keys.
- `fetchARC(ctx, path)` reads the file, parses it, stamps a `timestamp_ms`, and returns `*ARCMetrics`. It honors context cancellation (a stalled `/proc` mount must not hang the dashboard).
- `HandleARCAPI` exposes the result at `/api/arc` (GET only), following the same error/JSON conventions as `HandleZPoolAPI`.
- `resolveARCPath` always returns the fixed `/proc` path; unlike `zpool` there is no NixOS special-case because the proc entry is not affected by the per-profile binary layout.

## SysInfo Scraper

`sysinfo.go` is the largest scraper (~600 lines) and aggregates a fastfetch-style host snapshot from `/proc` and `/sys`:

- Parsers are split by source file: `parseOSRelease`, `parseMemInfo`, `parseCPUInfo`, `parseUptime`, `parseLoadAvg`, `parseBtime`, `parseDefaultRoute`, `parseIPv4ForIface`. Each takes raw bytes and returns scalar/struct fields, so they are unit-testable with canned fixtures (`testdata/fib_trie`, `testdata/route`, etc.).
- `fetchSysInfo(ctx)` orchestrates the reads, honors context cancellation, and returns `*SysInfo` with a `timestamp_ms`.
- `HandleSysInfoAPI` exposes the snapshot at `/api/sysinfo` (GET only).
- `resolveSysInfoPath(p)` is a trivial identity function — the proc/sys layout is fixed by the kernel, so (unlike `zpool`) there is no NixOS special-case.
- On non-Linux OSes the `runtime.GOOS` guards short-circuit most reads to empty values; the endpoint still responds 200 with whatever fields it could populate.

## Types and JSON Shape

`api.go` defines the structs that map `zpool list -v --json` output. Key non-obvious points:

- `ZPoolOutput.Pools` and `Pool.Vdevs` are `map[string]…` (not slices), as is `VDev.Vdevs`. Vdevs are recursive (a vdev can contain vdevs).
- `Prop.Source` is `map[string]string`, not a string, even though it is typically a single source.
- `Pool` and `VDev` **both embed `PoolProperties`** (size, allocated, free, fragmentation, capacity, health, etc.). When extending properties, update both struct definitions or the two views will drift.

`sysinfo.go` defines `SysInfo` plus a handful of smaller structs (CPU, memory, OS, network). All fields use plain scalar types (string/uint64/float64) so they round-trip cleanly through `encoding/json`.

## Conventions

- `gofmt -s` (simplify) is enforced via `make check`; keep struct field alignment gofmt-clean.
- `CGO_ENABLED=0` is set in CI, in the Makefile `check` target, and in goreleaser. Pure-Go build is a hard requirement (cross-compile to freebsd/darwin depends on it).
- Error handling pattern throughout `api.go`/`arc.go`/`sysinfo.go`: log via `log.Printf` with a leading emoji (`⚠️`), then write a JSON error body and return. Match this style when adding endpoints.
- HTTP handlers write `Content-Type` and call `w.WriteHeader` explicitly before `w.Write` / `json.NewEncoder(w).Encode`. Follow the same explicit order.
- No third-party dependencies are permitted by design ("Zero Dependencies" is a headline feature in `README.md`). Do not add requires to `go.mod` without explicit maintainer agreement.
- The README explicitly labels Go code as "human made/reviewed" and HTML/templates/README as "AI assisted". Keep Go changes minimal and idiomatic.

## Gotchas

- **`go.mod` declares `go 1.26.4`** — a recent Go version. Older toolchains will refuse to build. CI pins `1.26`.
- **`make check` does not run `golangci-lint` or `staticcheck`** (both are commented out in the Makefile). Run them manually if you want the fuller static-analysis pass.
- **The `zpool` binary must be executable by the server process** — typically requires root or membership in the `zfs`/`disk` group. On Linux this usually means read access to `/dev/zfs`. The dashboard will return `500` with a permissions hint otherwise.
- **CodeQL workflow targets `master`** (`.github/workflows/codeql.yml`) while the actual default branch is `main`, so CodeQL never runs on PRs/pushes. Update the branch filter if you want CodeQL coverage.
- **`.github/workflows/golang.yml` matrix includes `windows-latest`**; `go build ./...` will pass there (it just compiles the library) but the binary is useless on Windows. Do not assume green CI means "works on Windows".
- **`BIND_ADDR=0.0.0.0:…` exposes the dashboard to the network with no auth.** The README warns this requires a reverse proxy + auth. Keep the `127.0.0.1` default when adding examples.
- **No caching of `zpool` output.** Every `/api/zpool` request spawns a subprocess. High-frequency polling should be addressed by a background-refresh goroutine, not by hammering the endpoint.
- **The goreleaser `license` field says "BSD 3-Clause"** while `LICENSE` is MIT. Do not "fix" this blindly without confirming maintainer intent.
