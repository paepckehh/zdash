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
	addr := os.Getenv("BIND_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", zdash.HandleIndex)
	mux.HandleFunc("/api/zpool", zdash.HandleZPoolAPI)
	mux.HandleFunc("/api/arc", zdash.HandleARCAPI)
	mux.HandleFunc("/api/sysinfo", zdash.HandleSysInfoAPI)

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	log.Printf("zdash %s listening on %s", version, addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("⚠️  server failed: %v", err)
	}
}
