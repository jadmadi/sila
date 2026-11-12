//go:build !linux

package daemon

import "net"

// peerAllowed accepts peers on platforms without SO_PEERCRED.
func peerAllowed(conn net.Conn) bool { return true }
