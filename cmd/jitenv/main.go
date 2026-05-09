package main

import (
	"fmt"
	"os"
	"path/filepath"

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
	if base := filepath.Base(os.Args[0]); base != "jitenv" && base != "" {
		shim.Main(base, os.Args[1:])
		return
	}
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
