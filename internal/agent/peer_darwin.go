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
//
// The handler passes a net.Conn; on Unix the agent listener always
// produces *net.UnixConn, so the type assertion is total.
func checkPeerUid(c net.Conn) error {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("peer check: unexpected conn type %T", c)
	}
	raw, err := uc.SyscallConn()
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
