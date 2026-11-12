//go:build linux

package daemon

import (
	"net"
	"os"
	"syscall"
)

// peerAllowed checks SO_PEERCRED against the current UID.
func peerAllowed(conn net.Conn) bool {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return false
	}
	allowed := false
	_ = raw.Control(func(fd uintptr) {
		cred, credErr := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		if credErr != nil {
			return
		}
		allowed = int(cred.Uid) == os.Getuid()
	})
	return allowed
}
