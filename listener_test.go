package zdash

import (
	"net"
	"testing"
)

// Socket adoption is opt-in (SOCKET_ACTIVATION=1), so without the
// env var Listen must always bind its own socket, even when a stray
// LISTEN_FDS is present.
func TestListen_FallbackBind(t *testing.T) {
	t.Setenv("SOCKET_ACTIVATION", "")
	t.Setenv("LISTEN_FDS", "")
	ln, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	if _, ok := ln.(*net.TCPListener); !ok {
		t.Errorf("listener type = %T, want *net.TCPListener", ln)
	}
}

func TestListen_FallbackDespiteStrayListenFds(t *testing.T) {
	t.Setenv("SOCKET_ACTIVATION", "")
	t.Setenv("LISTEN_FDS", "1")
	t.Setenv("LISTEN_PID", "999999")
	ln, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	if _, ok := ln.(*net.TCPListener); !ok {
		t.Errorf("listener type = %T, want *net.TCPListener (adoption must be opt-in)", ln)
	}
}

func TestListen_BindError(t *testing.T) {
	t.Setenv("SOCKET_ACTIVATION", "")
	if _, err := Listen("256.256.256.256:1"); err == nil {
		t.Fatal("Listen with invalid address did not fail")
	}
}
