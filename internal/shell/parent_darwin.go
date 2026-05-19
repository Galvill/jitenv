//go:build darwin

package shell

import (
	"os"

	"golang.org/x/sys/unix"
)

// parentProcessName returns the executable basename of this process's
// parent on macOS. Uses sysctl(kern.proc.pid) via x/sys/unix —
// /proc/<pid>/comm doesn't exist on Darwin. The Proc.P_comm field is
// a 17-byte char array null-terminated at the actual name end.
//
// Used by DetectShellDetailed to identify the currently-running shell
// rather than the login shell from $SHELL (#164 follow-up).
//
// Returns "" on sysctl failure; the caller falls back to $SHELL.
func parentProcessName() string {
	proc, err := unix.SysctlKinfoProc("kern.proc.pid", os.Getppid())
	if err != nil || proc == nil {
		return ""
	}
	// P_comm is [17]int8 NUL-terminated.
	var sb []byte
	for _, c := range proc.Proc.P_comm {
		if c == 0 {
			break
		}
		sb = append(sb, byte(c))
	}
	return string(sb)
}
