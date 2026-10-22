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

`zdash` is an HTTP server that serves `zpool list -v --json` output either as raw JSON (`/api/zpool`) or as an embedded HTML dashboard (`/`). All HTML/CSS/JS is embedded into the binary via `embed.FS`; there is no frontend build step and no third-party Go dependencies (`go.mod` has zero requires). **Startup is zero-work**: the process only binds its listener and deep-sleeps; all scraping happens in a session-scoped background loop that runs only while at least one HTTP client is connected.

Module path: `paepcke.de/zdash`. Targets Linux, FreeBSD, and macOS (any OS with a `zpool` binary). The Go module line is currently `go 1.27.2`.

## Repository Layout

The tree is split into a library package at the repo root (`package zdash`) and a thin `cmd/zdash/` entrypoint (`package main`):

```
.
├── api.go            # zpool scraper + ZPoolOutput/Pool/VDev/Prop types, HandleIndex, HandleZPoolAPI, produceZpool
├── arc.go            # ARC kstat scraper (/proc/spl/kstat/zfs/arcstats), HandleARCAPI, produceARC
├── sysinfo.go        # Host snapshot scraper (/proc + /sys), HandleSysInfoAPI, produceSysInfo
├── smart.go         # smartctl scraper (--scan + parallel per-device --xall), HandleSmartAPI, produceSmart
├── idle.go          # session tracking via http.ConnState + runtime deep-sleep/wake profiles (idleGate)
├── refresher.go     # session-scoped background fetch loop + per-endpoint TTL cache + singleflight
├── listener.go      # portable Listen(): systemd fd adoption (Linux-only, opt-in) with BIND_ADDR fallback
├── listener_linux.go # Linux-only sd-listen-fds adoption, gated by SOCKET_ACTIVATION=1
├── listener_stub.go  # non-Linux no-op stub (FreeBSD/macOS/Windows always bind BIND_ADDR)
├── systemd.nix      # NixOS module: plain boot-started hardened service behind Caddy
├── systemd-socket.nix # NixOS module: socket-activated + exit-on-idle variant behind Caddy
├── api_test.go       # zpool JSON parsing tests (canned fixtures in testdata/)
├── arc_test.go       # ARC kstat parsing tests (canned fixtures in testdata/)
├── sysinfo_test.go   # /proc + /sys parser tests (canned fixtures in testdata/)
├── smart_test.go    # smartctl JSON parsing + handler tests (fixtures in testdata/)
├── idle_test.go      # idle gate transition tests (grace timer, wake/sleep cycles, exit-on-idle)
├── refresher_test.go # cache / singleflight / background-loop tests
├── listener_test.go  # portable fallback-bind tests (adoption stays disabled by default)
├── listener_linux_test.go # Linux-only adoption tests (env gating, LISTEN_FDS parsing, fd handling)
├── cmd/zdash/main.go # package main — wires http.Server + method-aware routes + ConnState hook + BootSleep
├── cmd/zdash/main_test.go # listenerAddr test (nil listener → "systemd socket")
├── embed/index.html  # single-file dashboard (HTML/CSS/JS), served via embed.FS
├── docs/            # README assets: hand-built theme-aware hero banners (hero_dark.svg / hero_light.svg, wired into a <picture> with prefers-color-scheme) + screenshots (system light/dark, pools, ARC, SMART NVMe/ATA)
├── testdata/         # canned fixtures: sample.json, arcstats, fib_trie, route, smart_{scan,sda,nvme}.json, ...
├── Makefile
├── .goreleaser.yml   # goreleaser v2 config: linux/freebsd/darwin × amd64/arm64, tar.gz + deb/rpm
└── .github/workflows/  # golang.yml (build/vet/test matrix), release.yml (goreleaser on v* tags), codeql.yml
```

`go build ./cmd/zdash` and `go test ./...` from the repo root both succeed today.

## Build, Test, Lint Commands

The test suites (`api_test.go`, `arc_test.go`, `sysinfo_test.go`, `smart_test.go`) use canned fixtures in `testdata/` (`sample.json` for zpool JSON, `arcstats` for the ARC kstat, `fib_trie` / `route` for the sysinfo network parser, `smart_scan.json` / `smart_sda.json` / `smart_nvme.json` for the SMART scraper).

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

- **CI** (`.github/workflows/golang.yml`): `CGO_ENABLED=0 go build ./...`, `go vet ./...`, and `go test ./...` (tests are skipped on the Windows runner because the subprocess-stub tests write `#!/bin/sh` scripts) across ubuntu/macos/windows. Go comes from `go-version-file: go.mod`, so the matrix always matches the module's toolchain line. Note: Windows is in the matrix even though the `zpool` binary is effectively never present there.

- **Releases** (`.github/workflows/release.yml`): triggered by `v*` tags (plus manual `workflow_dispatch`), runs goreleaser v2.18.2 (`goreleaser/goreleaser-action@v7`) with `release --clean` on ubuntu-24.04, Go from `go-version-file: go.mod`. The v2 config (`.goreleaser.yml`) runs `go mod tidy` + `go test ./...` as before-hooks, cross-compiles all six targets (linux/freebsd/darwin × amd64/arm64, `CGO_ENABLED=0`, `-trimpath -s -w`, version ldflags into both `main.version` and `zdash.Version`), packs directory-wrapped `tar.gz` archives (`zdash_<version>_<os>_<arch>.tar.gz` with LICENSE + README) plus `.deb`/`.rpm` (linux only, conventional file names, docs installed to `/usr/share/doc/zdash/`), writes `checksums.txt`, and cuts a non-draft release with `prerelease: auto`. Changelog excludes `docs:`/`test:`/`ci:`/`chore:`/`style:` commits. `mod_timestamp` is pinned to the commit timestamp for reproducible builds.

- **Local release dry run** (Makefile `release-snapshot` / `release-check` targets):
  ```bash
  make release-check      # goreleaser check — validate .goreleaser.yml
  make release-snapshot   # goreleaser release --snapshot --clean — full pipeline, publishes nothing (needs goreleaser v2 on $PATH)
  ```

- **Regenerate `go.mod`** (Makefile `deps` target): deletes and re-creates `go.mod`/`go.sum` via `go mod init paepcke.de/zdash && go mod tidy`. Only run if you intend to wipe the existing module file.

## Architecture and Data Flow

1. **Entry** (`cmd/zdash/main.go`): constructs an `http.Server` with explicit read/write/idle timeouts (read 10s + 5s read-header, write 30s, idle 30s) and registers method-aware routes (Go 1.22+ `"GET /…"` patterns, so non-GET requests get an automatic 405): `/` → `HandleIndex`, `/api/zpool` → `HandleZPoolAPI`, `/api/arc` → `HandleARCAPI`, `/api/sysinfo` → `HandleSysInfoAPI`, `/api/smart` → `HandleSmartAPI`. Bind address comes from `BIND_ADDR` (default `127.0.0.1:8080`) but is only the fallback: `zdash.Listen` first tries systemd socket activation — **Linux-only, and opt-in via `SOCKET_ACTIVATION=1`; disabled by default** so a stray `LISTEN_FDS` in the environment can never hijack fd 3. **Nothing is scraped at startup**: after registering routes, main calls `zdash.BootSleep()` and blocks in `srv.Serve(ln)` with the runtime parked (GC off, one OS thread). `http.Server.ConnState` is wired to `zdash.IdleConnState` for session tracking. With `EXIT_ON_IDLE=1` the process additionally terminates (exit 0) after the idle deep-sleep transition, letting a systemd `.socket` unit restart it on demand (see `systemd.nix`) so idle RAM/CPU are exactly zero.
2. **Session lifecycle** (`idle.go`): the package-level `idleGate` (`sessions`) counts live connections via the ConnState hook. First connection → wake: `GOMAXPROCS` restored, GC re-enabled (percent from `GOGC` env, default 100), background refresher started. Last connection closed → after a 3s grace period (cancellable by any reconnect) → deep sleep: the refresher is stopped and its cache dropped **first** (so those pages can actually be returned), then `debug.SetGCPercent(-1)`, `debug.FreeOSMemory()` (heap handed back to the OS), `runtime.GOMAXPROCS(1)`. Idle CPU while sleeping is effectively zero (only the listener epoll stays armed); the first new connection instantly wakes the process again. The connection counter is a lock-free atomic, deliberately outside the gate mutex: a connection accepted while the grace-timer callback holds the mutex must stay visible to `trySleep`, which re-checks `conns.Load()` after `sleepFn()` and before an exit-on-idle exit — never sleep or exit on top of an already-established connection (see `TestIdleGate_ConnectionDuringSleepIsNotLost`). Sessions are also capped at `maxSession` (4 minutes): the gate tracks live `net.Conn`s in `liveConns` and, when the session timer (`endSession`) fires, force-closes every one of them so the process can park (or exit under `EXIT_ON_IDLE` and let socket activation re-arm); clients transparently reconnect on their next poll, opening a fresh session (see `TestIdleGate_MaxSessionForceClose`). An endpoint that is still active and reconnects during or right after a forced session end keeps the session online: it is granted a fresh capped window via `armSessionLocked` (armed from both `wake()` and the tail of `endSession()`), so an active client is never left unserved and the gate is never awake without a session cap (see `TestIdleGate_ReconnectAfterSessionEndStaysCapped`); an already-armed window is never extended, so 4 minutes stays a hard ceiling for one continuously connected session. The counter is bumped before the gate mutex is taken for map bookkeeping, preserving the visibility guarantee above.
3. **Background refresher** (`refresher.go`): while a session is active, a loop (5s tick) refreshes every endpoint on its TTL schedule (zpool/arc/sysinfo 6s, smart 60s) so HTTP handlers only copy pre-encoded cached JSON bytes. Concurrent refreshes of one endpoint dedupe into a single fetch (singleflight); a handler hitting a stale/missing entry joins or triggers the in-flight fetch. Fetches run under `context.WithoutCancel` so they survive the disconnecting client that spawned them; waiters whose own context dies fall back to the newest stale snapshot or a 503 JSON error. On session end the loop's stop channel is closed (and stays closed — `get` checks it under the cache mutex so a tick that was already committed cannot start a new fetch for a dead session) and the entry map is cleared so the pages are returned to the OS. Fetches still in flight when a session ends complete but do not repopulate the dropped cache: the fetch goroutine stores its result only when the session generation it started under is still current (`r.gen`), so a late store cannot smuggle stale bytes into the next session.
4. **Producers**: each endpoint has a `produce*(ctx) (body []byte, status int)` function (`produceZpool` in `api.go`, `produceARC` in `arc.go`, `produceSysInfo` in `sysinfo.go`, `produceSmart` in `smart.go`) that owns its timeout (5s zpool/arc/sysinfo, 30s smart) and renders the exact cached bytes, including the endpoint-specific error bodies. The `Handle*API` handlers are thin wrappers over `serveRefresh(w, r, key)` in `refresher.go`; new endpoints register in both `ttls` and `defaultProducers`.
5. **`/` (HandleIndex)** — reads `embed/index.html` from the embedded FS once, applies the `__ZDASH_VERSION__` replacement once, caches the rendered page via `sync.OnceValue`, and copies the bytes per request.
6. **`/api/smart`** — `produceSmart` runs `smartctl --scan --json`, then probes every discovered device with `smartctl --xall --json <dev>` in parallel (one `sync.WaitGroup.Go` goroutine per device, each with a 12s per-device timeout under the 30s budget). Results land in the device's scan slot so the report order is deterministic without a lock. When smartctl is missing the endpoint still returns `200` with `smartctl_available=false` so the frontend can hide the SMART tabs.
7. **`zpool` binary resolution** (`api.go`): defaults to `zpool` on `$PATH`, but if `/run/current-system/sw/bin/zpool` exists it uses that instead. This is the NixOS-specific path — **do not remove this stat-check**, it is the only thing making the dashboard work on NixOS where `zpool` is not on the default `$PATH`. The same NixOS special-case exists for `smartctl` in `smart.go`.
8. **Client side** (`embed/index.html`, single file, ~2200 lines): vanilla JS, no framework, no external asset requests. Every poll runs `fetchSysInfo` first (the capability probe decides which tabs to fetch), then `/api/zpool`, `/api/arc`, and `/api/smart` in parallel via `Promise.all`; each request goes through `getJSON`, which adds a 35s `AbortSignal.timeout` and surfaces JSON error bodies. A `refreshing` latch prevents overlapping polls when a fetch is slow. The 5s timer pauses while the tab is hidden; on becoming visible the timer restarts AND an immediate refresh fires — the server's 30s IdleTimeout drops the dormant keep-alive socket in the meantime so the process returns to deep sleep. Rendering is **reactive mount-once/update-in-place**: cards are created once under a stable `data-key` (via `mountKeyed`) and the KPI strip mounts five fixed slots, then each poll only mutates text/width/attribute values through the `setTxt`/`setW`/`setBadge`/`updateDonut`/`updateSparkline` helpers. Donut gauges are SVG circles whose `stroke-dasharray`/`stroke-dashoffset` transition in CSS, so donuts and bars animate between polls instead of being rebuilt. When adding a card, follow this pattern: skeleton in `mountKeyed` + an update block of `set*` calls — do NOT rebuild `innerHTML` per poll (it kills the CSS transitions and reintroduces flicker). Whole leaf lists that change as a unit (the host key/value columns, the recursive vdev topology, the SMART pane) may rebuild their own container; everything with a `js-*` marker or a CSS transition must not. Status/tone classes are also swapped **in place** via `classList` (`setBadge` swaps only `b-*` classes, `setTone` only the tone classes): never write `element.className = …` on a mounted node — a full overwrite drops the node's `js-*` marker class, the next poll's `querySelector('.js-…')` misses, and the render throws (the historical "failed to load pool data / fragEl is null" crash was exactly this). The nav "live" pill flips to a red "offline" state when any enabled endpoint fails. Renders a tabbed dashboard (System / Pools / ARC Cache / per-drive SMART tabs) with donut charts, a window-scaled sparkline, a throttled matrix-rain dark-mode ambient, a live hit/miss stream, gradient KPI tiles with 3D perspective hover, and a user-switchable light/dark theme. Host/distro logos are official brand marks (single-path SVGs from simpleicons.org, embedded inline so the dashboard stays offline-capable; Tux is the fallback). History for the hit-ratio sparkline and the traffic stream is kept in-memory across refreshes. All dynamic strings interpolated into HTML go through `escapeHTML` (pool names, SMART model strings, host fields) — keep it that way.

## ARC Scraper

`arc.go` mirrors the zpool scraper but reads `/proc/spl/kstat/zfs/arcstats` (a text kstat file) instead of spawning a subprocess:

- `parseARCStats` decodes the kstat text format: line 1 is a header, line 2 is column names (`name type data`), lines 3+ are `<key> <type-int> <value>` rows. The `type` field is a small integer (4 = `KSTAT_DATA_UINT64`); rows whose middle field is not an integer are skipped. Unknown numeric keys are ignored so the parser keeps working when newer kernels add keys.
- `fetchARC(ctx, path)` reads the file, parses it, stamps a `timestamp_ms`, and returns `*ARCMetrics`. It honors context cancellation (a stalled `/proc` mount must not hang the dashboard).
- `HandleARCAPI` exposes the result at `/api/arc` (GET only), following the same error/JSON conventions as `HandleZPoolAPI`.
- The kstat path is the fixed `arcstatsPath` constant (`/proc/spl/kstat/zfs/arcstats`); unlike `zpool` there is no NixOS special-case because the proc entry is not affected by the per-profile binary layout.

## SysInfo Scraper

`sysinfo.go` is the largest scraper (~600 lines) and aggregates a fastfetch-style host snapshot from `/proc` and `/sys`:

- Parsers are split by source file: `parseOSRelease`, `parseMemInfo`, `parseCPUInfo`, `parseUptime`, `parseLoadAvg`, `parseBtime`, `parseDefaultRoute`, `parseIPv4ForIface`. Each takes raw bytes and returns scalar/struct fields, so they are unit-testable with canned fixtures (`testdata/fib_trie`, `testdata/route`, etc.).
- `fetchSysInfo(ctx)` orchestrates the reads, honors context cancellation, and returns `*SysInfo` with a `timestamp_ms`. `FQDN` is built by the `fqdn(host, domain)` helper from the short hostname plus `/proc/sys/kernel/domainname`, tolerating an already-qualified hostname and the kernel's `(none)`/empty domain sentinel.
- `HandleSysInfoAPI` exposes the snapshot at `/api/sysinfo` (GET only).
- Paths are fixed `/proc` + `/sys` constants — the layout is kernel-fixed, so (unlike `zpool`) there is no NixOS special-case and no resolver seam.
- On non-Linux OSes the `runtime.GOOS` guards short-circuit most reads to empty values; the endpoint still responds 200 with whatever fields it could populate.

## systemd deployment (systemd.nix / systemd-socket.nix)

The repo ships TWO alternative NixOS module fragments — self-contained (no external arguments; all addresses hardcode `127.0.0.1:9091`) and identically hardened, each fronted by a Caddy vhost `http://zdash` (plain HTTP: a single-label host cannot get a public ACME cert, so the vhost must carry an explicit `http://` scheme) that `reverse_proxy`ies to `127.0.0.1:9091`. Import exactly ONE of the two — importing both yields conflicting `services.zdash`/`services.caddy` definitions:

- **`systemd.nix` (plain)**: the service binds `127.0.0.1:9091` itself (`BIND_ADDR`), is `wantedBy = ["multi-user.target"]` so it starts at boot, and keeps running (deep-sleeps between sessions; no `EXIT_ON_IDLE`, no `SOCKET_ACTIVATION`).
- **`systemd-socket.nix` (socket-activated)**: `systemd.sockets.zdash` (wanted by `sockets.target`) holds the `ListenStream` socket; the service is not started at boot at all — the first dashboard connection activates it. The unit sets `SOCKET_ACTIVATION=1` to opt the binary into adopting the passed fd (the feature is disabled by default in the binary), and runs with `EXIT_ON_IDLE=1`, so it exits (status 0) once the last session closes; `StartLimitIntervalSec = 0` keeps the on-demand restarts from being rate-limited, and the service is deliberately NOT `wantedBy` anything (that would defeat socket activation).
- Hardening: `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`, full kernel/cgroup/hostname/clock protection, `NoNewPrivileges`, `LockPersonality`, `MemoryDenyWriteExecute`, `RestrictNamespaces`, `SystemCallFilter=@system-service`, `RestrictAddressFamilies` to inet/inet6/unix, `SocketBindDeny=any` with only `SocketBindAllow=tcp:9091` (covers the fallback-bind path; the socket-activation fd never binds), and `IPAddressDeny=any` with only loopback allowed (the Caddy reverse proxy on the same host).
- Deliberately NOT restricted (they would break `zpool`/`smartctl` as root): capability bounding set, `@privileged`/`@resources` syscall groups, `PrivateDevices` (raw disks and `/dev/zfs` must stay visible), and `ProcSubset` (`/proc/spl` kstats must stay readable).
- Resource caps: `Nice=10`, `CPUWeight=10`, `IOWeight=10` (a dashboard must never compete with the storage stack), `MemoryMax=128M`, `TasksMax=32` (main process plus parallel smartctl children). Both modules pin `GOMEMLIMIT=8MiB`, which is what makes the README's "under 10 MB while you watch" claim true by default (measured: 9.3 MB RSS active with the limit; ~11-12 MB active / ~6.9 MB boot-RSS without it on a 12-core host). The unit `Description` is "zDash System & ZFS Dashboard" (positioning: general system dashboard that is ZFS-aware, not a ZFS-only tool).

## Types and JSON Shape

`api.go` defines the structs that map `zpool list -v --json` output. Key non-obvious points:

- `ZPoolOutput.Pools` and `Pool.Vdevs` are `map[string]…` (not slices), as is `VDev.Vdevs`. Vdevs are recursive (a vdev can contain vdevs).
- `Prop.Source` is `map[string]string`, not a string, even though it is typically a single source.
- `Pool` and `VDev` **both embed `PoolProperties`** (size, allocated, free, fragmentation, capacity, health, etc.). When extending properties, update both struct definitions or the two views will drift.

`sysinfo.go` defines `SysInfo` plus a handful of smaller structs (CPU, memory, OS, network). All fields use plain scalar types (string/uint64/float64) so they round-trip cleanly through `encoding/json`.

## Conventions

- `gofmt -s` (simplify) is enforced via `make check`; keep struct field alignment gofmt-clean.
- `CGO_ENABLED=0` is set in CI, in the Makefile `check` target, and in goreleaser. Pure-Go build is a hard requirement (cross-compile to freebsd/darwin depends on it).
- Error handling pattern throughout `api.go`/`arc.go`/`sysinfo.go`/`smart.go`/`refresher.go`: producers log via `log.Printf` with a leading emoji (`⚠️`), then return a JSON error body plus the 500 status; `serveRefresh` writes `Content-Type` and calls `w.WriteHeader` explicitly before `w.Write`. Match this style when adding endpoints: write a `produce*` function and register it in `refresher.go` (`ttls` + `defaultProducers`).
- Handler tests must call `cache.flush()` before invoking a `Handle*API` GET, because responses are cached per endpoint for up to their TTL (smart = 60s) and would otherwise leak between tests.
- Producer seams for tests: `smartctlResolver` (smart.go) and `zpoolResolver` (api.go) are package-level vars over the respective `resolve*Bin` functions; tests swap them (`withSmartctlBin` / `withZpoolBin`) to run the full handler pipelines against stub shell scripts without a real `smartctl`/`zpool` install. `fetchZpool(ctx, bin)` / `fetchSmartScan` / `fetchSmartDevice` take the binary explicitly for the same reason.
- The dashboard (`embed/index.html`) resolves its four poll URLs **relative** to `document.baseURI` via the `api(name)` helper (`api/zpool`, `api/arc`, `api/sysinfo`, `api/smart`), not root-relative — so the dashboard also works when a reverse proxy serves it under a path prefix or subpath. Keep new fetches on `api(...)`.
- Environment-dependent paths in tests (real `zpool`/`smartctl`/cpufreq sysfs on the host) must skip themselves with `t.Skip` rather than fail (`TestHandleSmartAPI_SmartctlMissing`, `TestResolveZpoolBin`, `TestReadCPUFreqLimits_MissingSysfs` are the models).
- The deep-sleep runtime profile (GC off, FreeOSMemory, GOMAXPROCS 1) must never run inside the test binary: tests build their own `idleGate` with injected callbacks. `BootSleep()` is only called from `main`.
- No third-party dependencies are permitted by design ("Zero Dependencies" is a headline feature in `README.md`). Do not add requires to `go.mod` without explicit maintainer agreement.
- The README explicitly labels Go code as "human made/reviewed" and HTML/templates/README as "AI assisted". Keep Go changes minimal and idiomatic.
- README structure (keep it when editing): hero with `for-the-badge` shields (live CI badge from `.github/workflows/golang.yml`, release, MIT; static Go/OS/zero-deps/"Idle cost 0"/"Active RAM < 10 MB"), screenshots at `https://paepcke.de/zdash/screen_{light,dark}.png`, the **three-pillar positioning** in "What is zDash?" (1. general system dashboard for OS+hardware, 2. ZFS-aware unique feature — pools/disks/ARC showing how well ZFS works, 3. resource-aware coolest feature — socket-activated, zero passive cost), the "Zero-Cost Contract" section with a mermaid state diagram (SocketArmed/DeepSleep/Active, EXIT_ON_IDLE), the **measured** cost table (numbers re-measured for real, see the GOMEMLIMIT note above), the "Featherweight on the client" section (~128 KB page, no framework), native GitHub Mermaid diagrams, collapsible `<details>` sections (deep-sleep internals, unit hardening, FAQ, roadmap, star-history), the API table covering all five routes, and the env-var table covering `BIND_ADDR`/`SOCKET_ACTIVATION`/`EXIT_ON_IDLE` plus a `GOMEMLIMIT` note. When endpoints/env vars change, update those tables and the architecture walkthrough in the same change; keep the Go version badge in sync with the `go` line in `go.mod`. Do not add a Go Report Card badge (service sunset); CI + release badges are the live signals.

## Gotchas

- **`go.mod` declares `go 1.27.2`** — a recent Go version. Older toolchains will refuse to build. CI and the release workflow install Go via `go-version-file: go.mod`, so they always match.
- **The nix LSP may attach to non-nix files and report bogus `syntax_error` diagnostics on `.go`/`.html` files** — these are spurious; trust `go build`/`go vet` instead.
- **`make check` does not run `golangci-lint` or `staticcheck`** (both are commented out in the Makefile). Run them manually if you want the fuller static-analysis pass.
- **The `zpool` binary must be executable by the server process** — typically requires root or membership in the `zfs`/`disk` group. On Linux this usually means read access to `/dev/zfs`. The dashboard will return `500` with a permissions hint otherwise.
- **Card width classes must stay scoped to `.grid >`.** `.grid > .card` (specificity 0,2,0) would otherwise beat `.col-7`/`.col-12` (0,1,0) and collapse every wide card to half width. The width rules are `.grid > .col-N` for exactly this reason.
- **`.github/workflows/golang.yml` matrix includes `windows-latest`**; `go build ./...` will pass there (it just compiles the library) but the binary is useless on Windows. Do not assume green CI means "works on Windows".
- **`BIND_ADDR=0.0.0.0:…` exposes the dashboard to the network with no auth.** The README warns this requires a reverse proxy + auth. Keep the `127.0.0.1` default when adding examples.
- **API responses are session-scoped cached JSON.** `zpool` runs at most once per ~5s while a dashboard is open and `smartctl` at most once per 60s, no matter how many clients poll in parallel. The background loop only runs while at least one HTTP connection is alive; with no clients the process deep-sleeps and runs zero subprocesses.
- **A hidden browser tab keeps its keep-alive socket only for IdleTimeout (30s).** After that the server deep-sleeps; the tab's next poll (on becoming visible) reconnects and wakes it. Do not raise IdleTimeout much without considering this.
- **CodeQL now targets `main`** (`.github/workflows/codeql.yml`, actions v3) so it runs on pushes and PRs; keep the branch filter in sync with the default branch.
- **License fields differ between README and LICENSE.** `LICENSE` (and the goreleaser/nfpm `license: BSD-3-Clause` field, which matches it) is BSD 3-Clause, while the README badge and License section say MIT. Resolve with the maintainer before "fixing" either side.
