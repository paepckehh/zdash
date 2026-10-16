//go:build linux

package zdash

import (
	"log"
	"net"
	"os"
	"strconv"
)

// socketActivationEnv opts in to systemd socket activation. Adoption of
// the systemd-passed fd is disabled by default so a stray LISTEN_FDS in
// the environment can never hijack fd 3 of an ordinary zdash run.
const socketActivationEnv = "ZDASH_SOCKET_ACTIVATION"

// passedFiles keeps adopted socket-activation files referenced so their
// finalizers cannot close the underlying descriptors for the lifetime of
// the process.
var passedFiles []*os.File

// systemdListener adopts the listener socket passed by systemd socket
// activation (the LISTEN_FDS/LISTEN_PID protocol, fd 3), or returns nil
// when socket activation is not enabled, not requested, or not systemd.
func systemdListener() net.Listener {
	if os.Getenv(socketActivationEnv) != "1" {
		return nil
	}
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

// adoptListener turns a passed socket file into a usable listener, or
// returns nil when the fd is not a socket. Non-socket fds are closed so
// they cannot leak.
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
