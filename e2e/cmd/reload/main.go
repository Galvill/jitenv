// Command jitenv-e2e-reload sends an OpReload to a running jitenv
// agent. It mirrors what `internal/tui/tui.go:pingAgentReload` does
// after a successful TUI save, but as a standalone binary the e2e
// harness can invoke from a scenario step.
//
// The real production trigger lives inside the TUI (and the shell
// hook's mtime check that reconciles symlinks); neither is reachable
// from a non-interactive `docker exec`, so the harness ships its own
// tiny client. Resolves the agent socket via `agent.DefaultPaths()`
// — same fallback chain (XDG_RUNTIME_DIR → /tmp/jitenv-<uid>) as the
// real client.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/gv/jitenv/internal/agent"
)

func main() {
	paths, err := agent.DefaultPaths()
	if err != nil {
		die("agent paths: %v", err)
	}
	if _, err := os.Stat(paths.Socket); err != nil {
		die("agent socket %s not present: %v", paths.Socket, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := agent.NewClient(paths.Socket).Reload(ctx); err != nil {
		die("reload: %v", err)
	}
	fmt.Fprintln(os.Stdout, "agent reloaded")
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "jitenv-e2e-reload: "+format+"\n", args...)
	os.Exit(1)
}
