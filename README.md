<div align="center">

# ⚡ zDash

**A modern, self-hosted ZFS dashboard written in pure Go.**

One binary. Zero dependencies. Real-time pool, ARC, and host metrics — rendered in a glassmorphic, theme-switchable web UI right from your browser.

<p>
  <a href="https://github.com/paepckehh/zdash/releases"><img alt="Go Version" src="https://img.shields.io/badge/Go-1.26%2B-00ADD8?style=flat&logo=go&logoColor=white" /></a>
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/License-MIT-22b14c?style=flat" /></a>
  <a href="https://github.com/paepckehh/zdash/releases"><img alt="Release" src="https://img.shields.io/github/v/release/paepckehh/zdash?style=flat&label=Release&color=00BFFF" /></a>
  <img alt="Status" src="https://img.shields.io/badge/Status-Production%20Ready-brightgreen?style=flat" />
  <img alt="OS" src="https://img.shields.io/badge/OS-Linux%20%7C%20FreeBSD%20%7C%20macOS-00BFFF?style=flat" />
  <img alt="Deps" src="https://img.shields.io/badge/Dependencies-ZERO-22b14c?style=flat" />
</p>

<p>
  <a href="#-quick-start"><b>Quick Start</b></a> ·
  <a href="#-features"><b>Features</b></a> ·
  <a href="#-screenshots"><b>Screenshots</b></a> ·
  <a href="#-api"><b>API</b></a> ·
  <a href="#-build-from-source"><b>Build</b></a> ·
  <a href="#-configuration"><b>Config</b></a> ·
  <a href="#-contributing"><b>Contributing</b></a>
</p>

---

</div>

## 🎯 Why zDash?

- 📊 **Real-time ZFS Monitoring** — Parses `zpool list -v --json` to surface pool health, capacity, fragmentation, and full vdev/disk topology.
- ⚡ **ARC Cache Insights** — Reads `/proc/spl/kstat/zfs/arcstats` for hit ratio, MRU/MFU breakdown, memory usage, demand vs prefetch, and L2ARC stats.
- 🖥️ **System Information** — Aggregates a fastfetch-style host snapshot from `/proc` and `/sys`: OS/codename/variant/build ID, kernel, SMBIOS vendor/product/version, CPU model/vendor/cores/frequency, memory, swap, load averages, locale, default-route interface + IPv4, and process count.
- 🎨 **Glassmorphic, Theme-Switchable UI** — Tabbed dashboard with donut charts, sparklines, gradient KPI tiles, smooth animations, and a one-click light/dark theme toggle (business-console vs cyberpunk).
- ⚡ **Zero Dependencies** — Single statically-linked binary with all HTML/CSS/JS embedded via `embed.FS`. No frameworks, no build step, no `node_modules`.
- 🛡️ **Security-First Defaults** — Binds to `127.0.0.1:8080` by default, read-only operations, no interactive ZFS mutations — localhost stays local.
- 🚀 **Production-Ready** — Context timeouts, graceful degradation, and strict `http.Server` configuration prevent resource leaks and crashes.
- 🐧 **NixOS-Aware** — Auto-detects the per-profile `zpool` path at `/run/current-system/sw/bin/zpool`, so the dashboard Just Works™ on NixOS without PATH tricks.

<div align="center">

## 📸 Screenshots

<details open>
<summary><b>☀️ Light Mode</b></summary>
<br>

<!-- TODO: replace with light-mode screenshot -->
![zDash Dashboard — Light Mode](https://paepcke.de/zdash/screenshot.png)

</details>

<details open>
<summary><b>🌙 Dark Mode</b></summary>
<br>

<!-- TODO: replace with dark-mode screenshot -->
![zDash Dashboard — Dark Mode](https://paepcke.de/zdash/screenshot.png)

</details>

</div>

---

## 🚀 Quick Start

> **Prerequisites:** Go 1.26+ and a working `zpool` binary on a ZFS-compatible OS (Linux, FreeBSD, or macOS).

The fastest way to run zDash — no clone, no build, no install:

```bash
go run paepcke.de/zdash/cmd/zdash@latest
xdg-open http://localhost:8080
```

That's it. zDash is now serving your dashboard at `http://localhost:8080`.

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

## ⚙️ Configuration

| Environment Variable | Default          | Description                              |
|----------------------|------------------|------------------------------------------|
| `BIND_ADDR`          | `127.0.0.1:8080` | Host and port the HTTP server binds to   |

```bash
# Run with defaults (localhost only)
./zdash

# Bind to all interfaces on a custom port
BIND_ADDR=0.0.0.0:9090 ./zdash

# Run with the Go race detector (debugging)
BIND_ADDR=127.0.0.1:8080 go run -race ./cmd/zdash
```

> ⚠️ **`BIND_ADDR=0.0.0.0:…` exposes the dashboard to your network with no authentication.**
> Always front it with a reverse proxy (Nginx, Caddy, Traefik) that adds auth before binding to a non-loopback address.

---

## 🧩 Usage

Once running, open `http://<BIND_ADDR>` in your browser. The dashboard polls the API endpoints in the background and re-renders without page reloads. Switch tabs between **Pools**, **ARC Cache**, and **System**, and toggle the light/dark theme from the header.

---

## 🔌 API

All endpoints are `GET`-only and return `application/json` (except `/`, which returns the embedded dashboard HTML).

| Method | Path            | Description                                                           | Response Type      |
|--------|-----------------|-----------------------------------------------------------------------|--------------------|
| `GET`  | `/`             | Serves the embedded single-file dashboard                             | `text/html`        |
| `GET`  | `/api/zpool`    | Raw `zpool list -v --json` output, parsed into typed structs          | `application/json` |
| `GET`  | `/api/arc`      | ARC cache metrics parsed from `/proc/spl/kstat/zfs/arcstats`          | `application/json` |
| `GET`  | `/api/sysinfo`  | Host snapshot (OS, CPU, memory, network, SMBIOS) sourced from `/proc` + `/sys` | `application/json` |

Example:

```bash
curl -s http://localhost:8080/api/zpool | jq '.pools | keys'
```

---

## 🏗️ Architecture & Flow

```
                ┌──────────────────────────────────────────────┐
                │                  cmd/zdash                   │
                │   http.Server (10s/30s/120s timeouts)        │
                │   routes: /  /api/zpool  /api/arc  /api/sys  │
                └───────────────┬──────────────┬───────────────┘
                                │              │
   ┌────────────────────────────┘              └─────────────────────────┐
   ▼                                                                       ▼
┌─────────────────┐  ┌──────────────────┐  ┌──────────────────┐  ┌────────────────┐
│  HandleIndex    │  │ HandleZPoolAPI   │  │  HandleARCAPI    │  │ HandleSysInfo  │
│  embed/index.html│ │  zpool -v --json  │  │  /proc/.../arcstats│  │ /proc + /sys  │
└─────────────────┘  └──────────────────┘  └──────────────────┘  └────────────────┘
        ▲                     │                     │                     │
        │                     ▼                     ▼                     ▼
        │             ┌──────────────┐    ┌──────────────┐      ┌──────────────┐
        │             │  ZPoolOutput │    │  ARCMetrics  │      │   SysInfo    │
        │             └──────────────┘    └──────────────┘      └──────────────┘
        │                     │                     │                     │
        └─────────────────────┴─────────────────────┴─────────────────────┘
                              ▲
                              │
                    ┌──────────────────┐
    ┌───────────────│  embed/index.html│  ← vanilla JS, no framework
    │  polls /api/* │  ~1500 lines     │    donut charts, sparklines,
    │  in parallel  │  light/dark theme│    gradient KPI tiles
    └───────────────┴──────────────────┘
```

1. **Startup** — `cmd/zdash/main.go` constructs an `http.Server` with explicit read/write/idle timeouts and registers all four routes.
2. **Initial render** — `GET /` serves the embedded dashboard. No server-side templating; the client fetches its own data.
3. **Client-side refresh** — The dashboard polls `/api/zpool`, `/api/arc`, and `/api/sysinfo` in parallel on a timer and re-renders without reloading.
4. **Capability gating** — `/api/sysinfo` reports whether `/dev/zfs` and the ARC kstat are present, so the frontend suppresses the Pools and ARC Cache tabs on hosts without a ZFS kernel.
5. **Graceful degradation** — Failed subprocesses, unparseable files, and missing `/proc` or `/sys` entries degrade to empty fields or JSON error bodies; the server never crashes.

---

## 📦 Production Considerations

- **Security** — Exposing zDash to untrusted networks requires a reverse proxy (Nginx / Caddy / Traefik) with authentication. The default `127.0.0.1:8080` bind is safe.
- **Permissions** — The binary needs read access to `/dev/zfs` (typically via root or membership in the `zfs`/`disk` group). Without it, `/api/zpool` returns `500` with a permissions hint.
- **Performance** — Every `/api/zpool` request spawns a `zpool` subprocess. Ideal for internal/low-traffic dashboards. For high-frequency polling or large fleets, add a background-refresh goroutine or integrate with `systemd`/`zfs` events instead of hammering the endpoint.

---

## 🤝 Contributing

Contributions are welcome! Please:

1. Fork the repo and create a feature branch.
2. Run `make check` (gofmt + vet) before pushing.
3. Add or update tests in the matching `*_test.go` file using the canned fixtures in `testdata/`.
4. Open a pull request with a clear description of **what** and **why**.

> 🤖 **AI Policy** — Go code in this repo is **human-made and human-reviewed**. HTML templates, the README, and parsers are AI-assisted and labelled as such. Keep Go changes minimal and idiomatic.

---

## 📄 License

Distributed under the **MIT License**. See [`LICENSE`](LICENSE) for details.

---

<div align="center">

**Built with ❤️ using idiomatic Go, embedded assets, and modern web standards.**

[Quick Start](#-quick-start) · [Features](#-features) · [API](#-api) · [Build](#-build-from-source) · [Config](#-configuration) · [Contributing](#-contributing)

</div>