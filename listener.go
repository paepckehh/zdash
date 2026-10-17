package zdash

import (
	"fmt"
	"net"
)

// Listen returns the listener for the dashboard server. Under systemd
// socket activation (a .socket unit with ListenStream) systemd holds the
// bound socket and passes it as file descriptor 3 when activating the
// service. Adoption is opt-in and Linux-only: it requires
// SOCKET_ACTIVATION=1 in the environment, otherwise — and on every
// other platform — zdash always binds addr itself (e.g. BIND_ADDR).
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
