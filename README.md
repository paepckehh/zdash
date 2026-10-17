<div align="center">

# ⚡ zDash

**Zero-cost ZFS monitoring in a single pure-Go binary.**

One `go run`. Zero dependencies. Real-time pool health, ARC statistics, SMART drive telemetry and host metrics, rendered in a glassmorphic theme-switchable dashboard that **deep-sleeps the moment you close the tab**.

<p>
  <a href="https://github.com/paepckehh/zdash/actions/workflows/golang.yml"><img alt="CI" src="https://github.com/paepckehh/zdash/actions/workflows/golang.yml/badge.svg?style=for-the-badge" /></a>
  <a href="https://github.com/paepckehh/zdash/releases"><img alt="Release" src="https://img.shields.io/github/v/release/paepckehh/zdash?style=for-the-badge&color=00BFFF" /></a>
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/License-MIT-22b14c?style=for-the-badge" /></a>
</p>
<p>
  <img alt="Go" src="https://img.shields.io/badge/Go-1.27%2B-00ADD8?style=for-the-badge&logo=go&logoColor=white" />
  <img alt="OS" src="https://img.shields.io/badge/OS-Linux%20%7C%20FreeBSD%20%7C%20macOS-00BFFF?style=for-the-badge" />
  <img alt="Deps" src="https://img.shields.io/badge/Dependencies-ZERO-22b14c?style=for-the-badge" />
  <img alt="CGO" src="https://img.shields.io/badge/CGO-0%25%20%7C%20static%20binary-8A2BE2?style=for-the-badge" />
</p>

<p>
  <a href="#-quick-start"><b>Quick Start</b></a> ·
  <a href="#-watch-it-sleep"><b>The Sleep Engine</b></a> ·
  <a href="#-screenshots"><b>Screenshots</b></a> ·
  <a href="#-api"><b>API</b></a> ·
  <a href="#-architecture--flow"><b>Architecture</b></a> ·
  <a href="#-deployment-nixos--systemd"><b>Deployment</b></a> ·
  <a href="#-contributing"><b>Contributing</b></a>
</p>

---

</div>

## 🎯 Why zDash?

Most ZFS monitoring stacks want a Prometheus, a Grafana, an exporter and a couple of gigabytes of RSS before they show you a single donut chart. zDash lives at the other end of the spectrum:

- 📊 **Real-time pool telemetry** — parses `zpool list -v --json` into typed structs: health, capacity, fragmentation, and the full recursive vdev/disk topology.
- 💾 **SMART drive health** — probes every drive `smartctl` can see in parallel (12 s per-device budget) and surfaces temperatures, wear, power-on hours, and ATA/NVMe-specific attributes in per-drive tabs.
- ⚡ **ARC cache analytics** — reads the `/proc/spl/kstat/zfs/arcstats` kstat directly: hit ratio, MRU/MFU breakdown, demand vs prefetch, L2ARC, with a live hit/miss stream and history sparkline.
- 🖥️ **Host snapshot** — a fastfetch-style inventory from `/proc` + `/sys`: OS/codename, kernel, SMBIOS vendor/product, CPU model and clocks, memory, swap, load, uptime, default-route interface + IPv4.
- 😴 **The sleep engine** — zero-work startup, session-scoped background refresh, and a runtime that parks itself (GC off, heap returned to the OS, one OS thread) the moment the last browser tab closes. See [below](#-watch-it-sleep).
- 🎨 **Reactive single-file UI** — ~2,200 lines of vanilla JS/CSS/SVG, embedded via `embed.FS`. Cards are mounted once and updated in place, so donuts, bars and sparklines *animate between polls* instead of flickering.
- 🛡️ **Security-first defaults** — loopback bind, GET-only routes (automatic 405s), context timeouts everywhere, every dynamic string HTML-escaped, and deployment modules with a genuinely hardened systemd unit.
- 🐧 **NixOS-aware** — auto-detects the per-profile `zpool`/`smartctl` under `/run/current-system/sw/bin/`, so it Just Works™ on NixOS without PATH tricks.

<div align="center">

## 📸 Screenshots

| ☀️ Light Mode | 🌙 Dark Mode |
|:---:|:---:|
| ![zDash Dashboard — Light Mode](https://paepcke.de/zdash/screen_light.png) | ![zDash Dashboard — Dark Mode](https://paepcke.de/zdash/screen_dark.png) |

</div>

---

## 🚀 Quick Start

> **Prerequisites:** Go 1.27+ and a working `zpool` binary on a ZFS host (Linux, FreeBSD, macOS).

**Try it right now** — no clone, no build, no install:

```bash
go run paepcke.de/zdash/cmd/zdash@latest
xdg-open http://localhost:8080
```

**Install it permanently:**

```bash
go install paepcke.de/zdash/cmd/zdash@latest
```

**Or grab a prebuilt static binary** — `.deb`, `.rpm`, and tarballs for linux/freebsd/darwin on amd64/arm64 (all `CGO_ENABLED=0`, statically linked) from the [Releases](https://github.com/paepckehh/zdash/releases) page.

---

## 😴 Watch It Sleep

zDash was designed around one question: *what should a dashboard cost when nobody is looking?* The answer: **nothing**.

The process boots into deep sleep — it binds its listener and parks the runtime without running a single `zpool` or `smartctl` subprocess or reading a byte of `/proc`. The first HTTP connection instantly wakes it; when the last connection has been gone for 3 seconds, it sleeps again:

```mermaid
stateDiagram-v2
    [*] --> DeepSleep : bind listener, park the runtime
    DeepSleep --> Active : first client connects
    Active --> DeepSleep : last client gone for 3s
    DeepSleep --> [*] : EXIT_ON_IDLE=1 (exit 0)
    note right of DeepSleep
        GC disabled, heap returned to the OS,
        GOMAXPROCS=1, zero subprocesses
    end note
    note right of Active
        background refresher keeps every endpoint
        warm (TTL cache + singleflight dedup)
    end note
```

<details>
<summary><b>🛠️ What "deep sleep" actually does under the hood</b></summary>

- `debug.SetGCPercent(-1)` + `debug.FreeOSMemory()` — the GC is switched off and the whole heap is handed back to the OS.
- `runtime.GOMAXPROCS(1)` — all but one OS thread are parked.
- The background refresher stops and its cache is dropped; pages go back to the OS.
- Only the listener epoll stays armed — the first new connection wakes the process instantly, restoring `GOMAXPROCS` and the GC (`GOGC`-tunable).
- A late fetch that was still in flight when the session ended can never smuggle stale bytes into the next session (session-generation guard on every cache store).
- With `EXIT_ON_IDLE=1` the process exits (status 0) at the sleep transition — pair it with a systemd `.socket` unit and idle RAM/CPU are *exactly* zero; systemd restarts the binary on the next connection.

</details>

### 💰 The cost model

| | While you watch | While you don't |
|---|---|---|
| `zpool` runs | 1 per ~5 s — all open tabs share one TTL cache | **0** |
| `smartctl` sweeps | 1 per 60 s, all drives in parallel | **0** |
| CPU | a few ms per refresh cycle | **effectively zero** |
| RAM | comfortably inside a 128 MB cgroup cap | heap returned to the OS |
| Process | running, GC on, full parallelism | parked, GC off — or *gone entirely* with `EXIT_ON_IDLE=1` |

---

## 🔌 API

All endpoints are `GET`-only and return `application/json` (except `/`, which serves the embedded dashboard). Every response is pre-encoded by the session-scoped refresher, so handlers do nothing but copy cached bytes.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/` | Embedded single-file dashboard (`text/html`) |
| `GET` | `/api/zpool` | Pool health, capacity, fragmentation, recursive vdev/disk topology — typed `zpool list -v --json` |
| `GET` | `/api/arc` | ARC hit ratio, sizes, MRU/MFU, demand vs prefetch, L2ARC — from the ZFS kstat |
| `GET` | `/api/sysinfo` | Host snapshot: OS, kernel, SMBIOS, CPU, memory, load, network — from `/proc` + `/sys` |
| `GET` | `/api/smart` | Per-drive SMART health: temperatures, wear, power-on hours, ATA/NVMe attributes — from `smartctl` |

```bash
curl -s http://localhost:8080/api/zpool | jq '.pools | keys'
curl -s http://localhost:8080/api/smart | jq '.devices[] | {name, ok, temp: .details.temperature_current}'
```

> Tabs for capabilities the host lacks (no `smartctl`, no ZFS kernel) are hidden automatically — `/api/smart` reports `smartctl_available`, `/api/sysinfo` reports ZFS presence.

---

## 🏗️ Architecture & Flow

```mermaid
flowchart LR
    U["Browser<br>(single-file UI, polls every 5s)"] -->|"GET /api/*"| R["serveRefresh"]
    R --> C{"TTL cache warm?"}
    C -->|"yes: instant JSON"| U
    C -->|"stale or missing"| F["singleflight fetch<br>(deduped per endpoint)"]
    F --> Z["zpool list -v --json"]
    F --> A["/proc/spl/kstat/zfs/arcstats"]
    F --> Y["/proc + /sys snapshot"]
    F --> M["smartctl --scan + --xall"]
    Z --> F
    A --> F
    Y --> F
    M --> F
    F --> C
```

1. **Startup** — `cmd/zdash/main.go` builds an `http.Server` with explicit read/write/idle timeouts, registers Go 1.22 method-aware routes (non-GET gets an automatic 405), wires the `ConnState` session hook, and enters deep sleep. Nothing is scraped at boot.
2. **Session** — the first connection wakes the runtime and starts the refresher (`refresher.go`): a 5 s loop that keeps every endpoint warm on its TTL schedule (zpool/arc/sysinfo 6 s, SMART 60 s). The last closed connection stops the loop and drops the cache.
3. **Requests** — handlers answer from the warmed cache; concurrent refreshes of one endpoint dedupe into a single fetch (singleflight). Fetches survive disconnecting clients (`context.WithoutCancel`); waiters whose client dies fall back to the newest stale snapshot.
4. **Rendering** — the dashboard polls all four API endpoints in parallel every 5 s (paused while the tab is hidden). Cards mount once under stable keys, then only mutate in place — CSS transitions animate donuts, bars and sparklines between polls. The nav pill flips to a red *offline* state if any enabled endpoint fails.
5. **Degradation** — failed subprocesses, unparseable files, and missing `/proc`/`/sys` entries degrade to empty fields or JSON error bodies; the server never crashes.

<details>
<summary><b>🔍 Implementation notes (the fun parts)</b></summary>

- **Zero `innerHTML` rebuilds** — the UI is mount-once / update-in-place; every dynamic string (pool names, SMART model strings, host fields) goes through `escapeHTML` before interpolation.
- **Zero-dependency singleflight & TTL cache** — hand-rolled in ~200 lines instead of importing `golang.org/x/sync`; `go.mod` has zero requires.
- **Deterministic parallel SMART** — one goroutine per discovered device, results land in the device's scan slot, so the report order is stable without a lock.
- **Embedded everything** — the whole UI (including inline single-path brand SVGs from simpleicons.org) ships inside the binary via `embed.FS`, rendered once and cached with `sync.OnceValue`.
- **NixOS resolver seam** — `zpool`/`smartctl` are resolved via a `stat` check for `/run/current-system/sw/bin/…` because NixOS does not put them on the default `$PATH`.

</details>

---

## ⚙️ Configuration

| Environment Variable | Default | Description |
|----------------------|---------|-------------|
| `BIND_ADDR` | `127.0.0.1:8080` | Fallback listen address (used only when no systemd fd is adopted) |
| `SOCKET_ACTIVATION` | `0` | Set `1` to adopt the fd systemd passes via `LISTEN_FDS` (Linux only, opt-in — a stray `LISTEN_FDS` can never hijack fd 3) |
| `EXIT_ON_IDLE` | `0` | Set `1` to `exit(0)` once the last session closes (pair with a systemd `.socket` unit) |

```bash
./zdash                                   # defaults: localhost only
BIND_ADDR=0.0.0.0:9090 ./zdash           # custom port, all interfaces
BIND_ADDR=127.0.0.1:8080 go run -race ./cmd/zdash   # with the race detector
```

> ⚠️ **`BIND_ADDR=0.0.0.0:…` exposes the dashboard to your network with no authentication.**
> Always front it with a reverse proxy (Caddy, Nginx, Traefik) that adds auth before binding to a non-loopback address.

---

## 🖥️ Deployment: NixOS + systemd

The repo ships **two alternative, identically hardened NixOS modules** (self-contained, all addresses `127.0.0.1:9091`, fronted by a Caddy vhost `http://zdash` that reverse-proxies to the service). Import exactly **one** of them:

```nix
# configuration.nix
imports = [ ./zdash/systemd.nix ];          # starts at boot, deep-sleeps between sessions
# imports = [ ./zdash/systemd-socket.nix ]; # socket-activated: zero idle cost, exits when idle
```

- **`systemd.nix` (plain)** — wanted by `multi-user.target`, binds `127.0.0.1:9091` itself, deep-sleeps between sessions. Idle CPU ≈ 0.
- **`systemd-socket.nix` (socket-activated)** — `systemd.sockets.zdash` (wanted by `sockets.target`) holds the socket; the service is not started at boot at all. The first dashboard connection activates it, the binary adopts the passed fd (`SOCKET_ACTIVATION=1`) and exits (status 0) once the last session closes (`EXIT_ON_IDLE=1`). `StartLimitIntervalSec = 0` keeps the on-demand restarts from ever being rate-limited. Idle RAM/CPU: exactly zero.

<details>
<summary><b>🛡️ Unit hardening highlights</b></summary>

- `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`, `NoNewPrivileges`, `LockPersonality`, `MemoryDenyWriteExecute`, `RestrictNamespaces`
- `SystemCallFilter=@system-service`, `RestrictAddressFamilies` limited to inet/inet6/unix
- `SocketBindDeny=any` with only `SocketBindAllow=tcp:9091`, `IPAddressDeny=any` with loopback allowed (the Caddy reverse proxy lives on the same host)
- Resource caps: `MemoryMax=128M`, `TasksMax=32` (main process plus parallel smartctl children), and `Nice=10` / `CPUWeight=10` / `IOWeight=10` — a dashboard must never compete with the storage stack it monitors
- Deliberately **not** restricted: capability bounding set, `@privileged`/`@resources` syscall groups, `PrivateDevices`, `ProcSubset` — `zpool`/`smartctl` need raw disks, `/dev/zfs`, and `/proc/spl` kstats

</details>

---

## 🛠️ Build from Source

```bash
git clone https://github.com/paepckehh/zdash.git
cd zdash

# the easy way (wraps go build + version ldflags)
make build

# ...or build directly
go build -C cmd/zdash -o zdash \
  -ldflags="-X main.version=$(git describe --tags --abbrev=0 2>/dev/null || echo 'dev')"

./zdash
```

---

<details>
<summary><b>❓ FAQ</b></summary>

- **The Pools tab shows a 500 error** — the process needs read access to `/dev/zfs` (typically root, or membership in the `zfs`/`disk` group). The error body includes a permissions hint.
- **No SMART tabs** — `smartctl` is missing or not permitted for this user; check `smartctl_available` in `/api/smart`. The endpoint still returns `200` so the UI can hide the tabs gracefully.
- **Running on a host without ZFS** — the server responds `200` everywhere; the dashboard simply hides the Pools/ARC tabs.
- **Windows** — the library compiles, but there is no `zpool` on Windows; treat it as a build-matrix curiosity, not a target.
- **Why does my hidden browser tab re-wake the server?** — the server drops dormant keep-alive sockets after its 30 s idle timeout and deep-sleeps; the tab's next poll on becoming visible reconnects instantly and wakes it.

</details>

<details>
<summary><b>🗺️ Roadmap (ideas, not promises)</b></summary>

- Prometheus `/metrics` endpoint
- Persistent history across restarts (long-horizon sparklines)
- Pool health-change notifications
- Multi-host aggregation view

</details>

<details>
<summary><b>⭐ Star History</b></summary>

[![Star History Chart](https://api.star-history.com/svg?repos=paepckehh/zdash&type=Date)](https://star-history.com/#paepckehh/zdash&Date)

</details>

---

## 🤝 Contributing

Contributions are welcome! Please:

1. Fork the repo and create a feature branch.
2. Run `make check` (gofmt + vet) before pushing.
3. Add or update tests in the matching `*_test.go` file using the canned fixtures in `testdata/`.
4. Open a pull request with a clear description of **what** and **why**.

> 🤖 **AI Policy** — Go code in this repo is **human-made and human-reviewed**. HTML, the README, and parsers are AI-assisted and labelled as such. Keep Go changes minimal and idiomatic.

---

## 🙏 Acknowledgements

- Distro/host logos: single-path SVGs from [simpleicons.org](https://simpleicons.org), embedded inline so the dashboard stays fully offline-capable (Tux is the fallback).
- ZFS, OpenZFS, and the `zpool --json`/kstat authors, for making all of this data so pleasant to consume.

## 📄 License

Distributed under the **MIT License**. See [`LICENSE`](LICENSE) for details.

---

<div align="center">

**Built with ❤️ using idiomatic Go, embedded assets, and modern web standards — no frameworks, no build step, no node_modules.**

[Quick Start](#-quick-start) · [The Sleep Engine](#-watch-it-sleep) · [API](#-api) · [Architecture](#-architecture--flow) · [Deployment](#-deployment-nixos--systemd) · [Contributing](#-contributing)

</div>
