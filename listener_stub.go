//go:build !linux

package zdash

import "net"

// systemdListener is a no-op everywhere except Linux: the systemd socket
// activation protocol is Linux-only, so zdash on FreeBSD/macOS/Windows
// always binds its own listener (BIND_ADDR).
func systemdListener() net.Listener { return nil }
