//go:build windows

package shim

import "errors"

// execReal on Windows refuses to run. The cwd_glob wrapper-symlink
// model assumes a POSIX exec-in-place primitive (so argv[0] survives
// and the parent shell's pid is inherited); Windows has no equivalent.
// A real port will need spawn-and-wait or a different invocation
// model entirely. Tracking in #39 stage 2+.
func execReal(_ string, _ []string, _ []string) error {
	return errors.New("jitenv shim: not yet supported on windows; tracking in #39")
}
