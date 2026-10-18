package main

import (
	"net"
	"testing"
)

func TestListenerAddr(t *testing.T) {
	if got := listenerAddr(nil); got != "systemd socket" {
		t.Errorf("listenerAddr(nil) = %q, want \"systemd socket\"", got)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	if got, want := listenerAddr(ln), ln.Addr().String(); got != want {
		t.Errorf("listenerAddr = %q, want %q", got, want)
	}
}
