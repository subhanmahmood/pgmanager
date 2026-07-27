//go:build !linux

package api

import "net"

// peerIdentity has no portable implementation; on non-Linux platforms the
// audit log records the connection as simply "local".
func peerIdentity(c net.Conn) string { return "local" }
