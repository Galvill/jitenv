// Package runnotice formats and gates the optional "jitenv: injected
// N variable(s)" stderr line printed before a mapped command execs.
// Shared by `internal/run` (path-mapped flow) and `internal/shim`
// (cwd_glob wrapper flow) so both honour the same opt-in setting and
// produce the same wording.
package runnotice

import (
	"fmt"
	"io"
	"os"

	"github.com/gv/jitenv/internal/config"
)

const (
	ansiGreen = "\033[32m"
	ansiReset = "\033[0m"
)

// Enabled returns whether the pre-run notice should be printed.
//
// Resolution order, first match wins:
//
//  1. JITENV_NO_NOTICE set to a non-empty value → off. Per-invocation
//     escape hatch for scripts and one-off commands; no config edit
//     required.
//  2. CI set to a non-empty value → off. Convention used by GitHub
//     Actions, GitLab CI, CircleCI, Travis — pipelines that pipe
//     credentials through jitenv don't want a stderr line per call.
//  3. agent.pre_run_notice in config.toml. Default is on (the helper
//     PreRunNoticeEnabled returns true for a missing key); explicit
//     false suppresses.
//  4. Config-load failure → off. A broken config should never start
//     surfacing surprise output to the user's terminal.
//
// The flag is plaintext TOML, so no master key is needed.
func Enabled() bool {
	if os.Getenv("JITENV_NO_NOTICE") != "" {
		return false
	}
	if os.Getenv("CI") != "" {
		return false
	}
	cfgPath, err := config.Resolve(os.Getenv("JITENV_CONFIG"))
	if err != nil {
		return false
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return false
	}
	return cfg.Agent.PreRunNoticeEnabled()
}

// Write formats the notice line and writes it to w. The caller is
// responsible for skipping the call when n == 0 (callers already
// have the count handy and an inline guard reads more obviously than
// a hidden gate here). When tty is true the message is wrapped in
// green ANSI escapes; otherwise plain text is emitted, so log files
// and CI captures stay clean.
func Write(w io.Writer, n int, tty bool) {
	noun := "variables"
	if n == 1 {
		noun = "variable"
	}
	msg := fmt.Sprintf("jitenv: injected %d %s", n, noun)
	if tty {
		fmt.Fprintf(w, "%s%s%s\n", ansiGreen, msg, ansiReset)
		return
	}
	fmt.Fprintln(w, msg)
}
