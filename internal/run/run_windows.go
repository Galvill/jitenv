//go:build windows

package run

import "errors"

// replaceProcess on Windows refuses to exec. Windows has no exec-in-
// place primitive that drops the current process image, so a real
// port will need a different model (spawn + wait, or token-stuffed
// CreateProcess). Tracking in #39 stage 2+.
func replaceProcess(_ string, _ []string, _ []string) error {
	return errors.New("jitenv run: not yet supported on windows; tracking in #39")
}
