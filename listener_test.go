package zdash

import (
	"net"
	"os"
	"testing"
)

func TestListen_FallbackBind(t *testing.T) {
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

func TestSystemdListener_NotActivated(t *testing.T) {
	t.Setenv("LISTEN_FDS", "")
	if ln := systemdListener(); ln != nil {
		ln.Close()
		t.Fatal("systemdListener returned a listener without LISTEN_FDS")
	}
}

func TestSystemdListener_PIDMismatch(t *testing.T) {
	t.Setenv("LISTEN_FDS", "1")
	t.Setenv("LISTEN_PID", "1") // not this process
	if ln := systemdListener(); ln != nil {
		ln.Close()
		t.Fatal("systemdListener adopted fd 3 despite LISTEN_PID mismatch")
	}
}

func TestSystemdListener_GarbageEnv(t *testing.T) {
	for _, tc := range []struct{ fds, pid string }{
		{"garbage", ""},
		{"-1", ""},
		{"1", "garbage"},
	} {
		t.Setenv("LISTEN_FDS", tc.fds)
		t.Setenv("LISTEN_PID", tc.pid)
		if ln := systemdListener(); ln != nil {
			ln.Close()
			t.Errorf("systemdListener returned a listener for LISTEN_FDS=%q LISTEN_PID=%q", tc.fds, tc.pid)
		}
	}
	_ = os.Unsetenv("LISTEN_FDS")
}

// TestAdoptListener_FromFile exercises the adoption path with a real socket
// passed as an *os.File, the same shape systemd hands the process as fd 3.
func TestAdoptListener_FromFile(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	f, err := ln.(*net.TCPListener).File()
	if err != nil {
		t.Fatalf("File(): %v", err)
	}
	adopted := adoptListener(f)
	if adopted == nil {
		t.Fatal("adoptListener did not adopt the socket file")
	}
	defer adopted.Close()
	if adopted.Addr().String() != ln.Addr().String() {
		t.Errorf("adopted addr = %s, want %s", adopted.Addr(), ln.Addr())
	}
}

// TestAdoptListener_NotASocket verifies the failure path: a plain file fd
// is rejected and closed instead of crashing or leaking.
func TestAdoptListener_NotASocket(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "notasock")
	if err != nil {
		t.Fatalf("tempfile: %v", err)
	}
	if ln := adoptListener(f); ln != nil {
		ln.Close()
		t.Fatal("adoptListener adopted a non-socket file")
	}
	if _, err := f.Stat(); err == nil {
		_ = f.Close()
		t.Error("failed adoption did not close the file")
	}
}
