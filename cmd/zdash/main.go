package main

import (
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"paepcke.de/zdash"
)

// version is injected at build time via -ldflags="-X main.version=...".
var version = "dev"

func main() {
	// Surface the linked version to the zdash package so HandleIndex can
	// stamp it into the dashboard HTML. When the build injected the value
	// directly into paepcke.de/zdash.Version via ldflags this is a no-op.
	if version != "dev" {
		zdash.Version = version
	}

	addr := os.Getenv("BIND_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", zdash.HandleIndex)
	mux.HandleFunc("GET /api/zpool", zdash.HandleZPoolAPI)
	mux.HandleFunc("GET /api/arc", zdash.HandleARCAPI)
	mux.HandleFunc("GET /api/sysinfo", zdash.HandleSysInfoAPI)
	mux.HandleFunc("GET /api/smart", zdash.HandleSmartAPI)

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
		// Connection tracking drives the runtime sleep profile: the first
		// connection wakes the process, the last closed connection puts it
		// back into deep sleep after a short grace period.
		ConnState:         zdash.IdleConnState,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		// IdleTimeout drops dormant keep-alive sockets quickly so the
		// server can go back to sleep while a dashboard tab is merely
		// hidden (the frontend pauses polling while hidden; the socket
		// idles out here and the process parks until the tab returns).
		IdleTimeout: 30 * time.Second,
	}

	// Under systemd socket activation systemd holds the bound socket and
	// starts this process on the first incoming connection; Listen adopts
	// that socket. Otherwise it binds BIND_ADDR (default 127.0.0.1:8080).
	ln, err := zdash.Listen(addr)
	if err != nil {
		log.Fatalf("⚠️  listen failed: %v", err)
	}
	if os.Getenv("EXIT_ON_IDLE") == "1" {
		// Exit instead of deep-sleeping when the last session closes; the
		// socket unit restarts the service on demand, so idle RAM/CPU
		// drop to zero.
		zdash.ExitOnIdle()
	}

	// Nothing is fetched at startup: no zpool run, no smartctl probe, no
	// /proc read. Park the runtime and serve.
	zdash.BootSleep()
	log.Printf("zdash %s listening on %s (deep-sleep until first connection)", version, listenerAddr(ln))
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("⚠️  server failed: %v", err)
	}
}

// listenerAddr renders the listener address for the startup log.
func listenerAddr(ln net.Listener) string {
	if ln == nil {
		return "systemd socket"
	}
	return ln.Addr().String()
}
