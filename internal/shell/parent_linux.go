//go:build linux

package shell

import (
	"fmt"
	"os"
	"strings"
)

// parentProcessName returns the executable basename of this process's
// parent on Linux. Read from /proc/<getppid>/comm — kernel-managed,
// always present on a running process, and trimmed to 16 bytes by the
// kernel which matches the longest shell name we care about.
//
// Used by DetectShellDetailed to identify the currently-running shell:
// $SHELL is the login shell from /etc/passwd, not the shell that
// actually invoked jitenv. A user whose login shell is bash but who
// runs `fish -c 'jitenv unlock'` would otherwise see "$SHELL says
// bash, all good" and miss the unsupported-shell warning (#164
// follow-up).
//
// Returns "" on read failure or empty result; the caller falls back to
// $SHELL detection.
func parentProcessName() string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", os.Getppid()))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
