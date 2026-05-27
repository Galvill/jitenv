package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/version"
	"github.com/gv/jitenv/internal/versioncheck"
)

const (
	ansiYellow = "\033[33m"
	ansiReset  = "\033[0m"
)

// newVersionNoticeInternalCmd is the foreground half of #136. The
// shell hook runs it once per shell-load — it reads the cache
// sidecar and prints a single yellow stderr line when a newer
// release is known. No network I/O, no config decryption; the
// command is fast enough to sit on the shell-startup hot path.
//
//	jitenv __version_notice
//
// Exit code is always zero so a hook-line "or" guard never breaks.
func newVersionNoticeInternalCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "__version_notice",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			writeVersionNotice(cmd.ErrOrStderr())
			return nil
		},
	}
}

// writeVersionNotice prints the upgrade-available line when the
// cached "latest" is newer than the running binary's version. Same
// opt-out / suppression rules as versionCheckPermitted so the two
// commands agree — no edge case where the background fetch is on
// but the foreground notice is off (or vice versa).
func writeVersionNotice(errw io.Writer) {
	if !versionCheckPermitted(asWriter(errw)) {
		return
	}
	path := versioncheck.Path()
	if path == "" {
		return
	}
	c, err := versioncheck.Load(path)
	if err != nil || c.Latest == "" {
		return
	}
	if !versioncheck.Newer(c.Latest, version.Version) {
		return
	}

	msg := fmt.Sprintf(
		"jitenv: %s is available (you have %s). Download from https://github.com/Galvill/jitenv/releases/latest",
		c.Latest, version.Version,
	)
	if stderrIsTTY() {
		fmt.Fprintf(errw, "%s%s%s\n", ansiYellow, msg, ansiReset)
		return
	}
	fmt.Fprintln(errw, msg)
}

// asWriter narrows an io.Writer to the interface debugf accepts.
// io.Writer's Write returns (int, error); debugf's parameter only
// needs Write, so a direct pass would compile — but the version_check
// file uses a stricter inline interface signature, so the conversion
// is explicit here.
func asWriter(w io.Writer) interface{ Write(p []byte) (int, error) } {
	return w
}

// Suppress the unused-import lint complaint from environments where
// os isn't otherwise referenced in this file. os.Stderr access lives
// inside writeVersionNotice's caller via cmd.ErrOrStderr.
var _ = os.Stderr
