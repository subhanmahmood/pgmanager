//go:build linux

package api

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// peerIdentity reads the uid/pid of the process on the other end of a unix
// socket connection. It is used for the audit log only — the authorization
// decision is made by the socket's file permissions, not by this value.
func peerIdentity(c net.Conn) string {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return "local"
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return "local"
	}
	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil || credErr != nil || cred == nil {
		return "local"
	}
	return fmt.Sprintf("local:uid=%d,pid=%d", cred.Uid, cred.Pid)
}
