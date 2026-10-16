package main

import (
	"log"
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

	// Nothing is fetched at startup: no zpool run, no smartctl probe, no
	// /proc read. Bind the listener, park the runtime, and wait.
	zdash.BootSleep()
	log.Printf("zdash %s listening on %s (deep-sleep until first connection)", version, addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("⚠️  server failed: %v", err)
	}
}
