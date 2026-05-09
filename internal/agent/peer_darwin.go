//go:build darwin

package agent

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// checkPeerUid enforces that the connecting client runs as the same uid.
// Darwin: read LOCAL_PEERCRED via getsockopt, returning the peer's
// effective uid in xucred.Uid (xucred.Cr_ngroups + Groups[0..N] also
// available but we don't need them).
func checkPeerUid(c *net.UnixConn) error {
	raw, err := c.SyscallConn()
	if err != nil {
		return err
	}
	var xu *unix.Xucred
	var sysErr error
	err = raw.Control(func(fd uintptr) {
		xu, sysErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	})
	if err != nil {
		return err
	}
	if sysErr != nil {
		return sysErr
	}
	if int(xu.Uid) != os.Getuid() {
		return fmt.Errorf("peer uid %d != %d", xu.Uid, os.Getuid())
	}
	return nil
}
