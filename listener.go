package zdash

import (
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
)

// Listen returns the listener for the dashboard server. Under systemd
// socket activation (a .socket unit with ListenStream) systemd holds the
// bound socket and passes it as file descriptor 3 when activating the
// service; zdash adopts that socket instead of binding its own. Without
// socket activation it falls back to binding addr (e.g. BIND_ADDR).
func Listen(addr string) (net.Listener, error) {
	if ln := systemdListener(); ln != nil {
		return ln, nil
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("bind %s: %w", addr, err)
	}
	return ln, nil
}

// passedFiles keeps adopted socket-activation files referenced so their
// finalizers cannot close the underlying descriptors for the lifetime of
// the process.
var passedFiles []*os.File

// systemdListener adopts the listener socket passed by systemd socket
// activation (the LISTEN_FDS/LISTEN_PID protocol, fd 3), or returns nil
// when the process was not socket-activated.
func systemdListener() net.Listener {
	if !activatedBySystemd() {
		return nil
	}
	return adoptListener(os.NewFile(uintptr(3), "systemd-socket"))
}

// activatedBySystemd reports whether systemd passed this process at least
// one socket fd (and that the fds belong to this pid, not a parent).
func activatedBySystemd() bool {
	fds, err := strconv.Atoi(os.Getenv("LISTEN_FDS"))
	if err != nil || fds < 1 {
		return false
	}
	pid, err := strconv.Atoi(os.Getenv("LISTEN_PID"))
	return err == nil && pid == os.Getpid()
}

// adoptListener turns a passed socket file into a net.Listener, or returns
// nil when the fd is not a socket.
func adoptListener(f *os.File) net.Listener {
	ln, err := net.FileListener(f)
	if err != nil {
		log.Printf("⚠️  systemd socket activation failed: %v", err)
		_ = f.Close()
		return nil
	}
	passedFiles = append(passedFiles, f)
	return ln
}
