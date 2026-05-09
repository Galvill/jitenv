//go:build linux

package agent

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// checkPeerUid enforces that the connecting client runs as the same uid.
// Linux: read SO_PEERCRED via getsockopt.
func checkPeerUid(c *net.UnixConn) error {
	raw, err := c.SyscallConn()
	if err != nil {
		return err
	}
	var ucred *unix.Ucred
	var sysErr error
	err = raw.Control(func(fd uintptr) {
		ucred, sysErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if err != nil {
		return err
	}
	if sysErr != nil {
		return sysErr
	}
	if int(ucred.Uid) != os.Getuid() {
		return fmt.Errorf("peer uid %d != %d", ucred.Uid, os.Getuid())
	}
	return nil
}
