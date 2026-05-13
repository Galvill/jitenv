package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gv/jitenv/internal/cli"
	"github.com/gv/jitenv/internal/shim"
	_ "github.com/gv/jitenv/internal/sources/builtin"
)

func main() {
	// When invoked under any name other than "jitenv", we are running
	// as a wrapper symlink for a cwd_glob mapping (e.g.
	// ~/.cache/jitenv/shells/<pid>/bin/npm → jitenv). Dispatch to the
	// shim entrypoint without going through cobra so cold start stays
	// fast for these per-command wrappings.
	//
	// On Windows the executable is "jitenv.exe", not "jitenv", so strip
	// the .exe suffix before the comparison — otherwise every direct
	// `jitenv.exe ...` invocation would dispatch to the shim and try to
	// re-find itself on $PATH.
	base := filepath.Base(os.Args[0])
	if name := strings.TrimSuffix(base, ".exe"); name != "" {
		base = name
	}
	if base != "jitenv" && base != "" {
		shim.Main(base, os.Args[1:])
		return
	}
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
