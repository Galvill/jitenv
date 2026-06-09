package agent

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// resolveAgentExecutable returns the path to the binary SpawnDaemon should
// re-exec as the long-lived `__agent` daemon. It is normally just
// os.Executable(), but when the currently-running binary is the lightweight
// `jitenv-hook` (#263) — which deliberately omits the agent/TUI/source graph
// and has no `__agent` subcommand — it resolves the sibling full `jitenv`
// binary installed alongside it.
//
// This mirrors, in the opposite direction, the resolution in
// internal/shell/render.go:hookBin, which prefers `jitenv-hook` next to
// `jitenv` and falls back when missing. Here we prefer `jitenv` next to
// `jitenv-hook` because only the full binary can serve as the agent.
//
// Without this, inline unlock (#264/#268) re-execs jitenv-hook, the child
// hits its default switch case (`unknown command "__agent"`), exits 2, and
// the socket-appearance loop times out.
func resolveAgentExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	base := filepath.Base(exe)
	if base == "jitenv-hook" || base == "jitenv-hook.exe" {
		name := "jitenv"
		if runtime.GOOS == "windows" {
			name = "jitenv.exe"
		}
		cand := filepath.Join(filepath.Dir(exe), name)
		if _, err := os.Stat(cand); err != nil {
			return "", fmt.Errorf("inline unlock needs the full jitenv binary alongside jitenv-hook (looked for %s): %w", cand, err)
		}
		exe = cand
	}
	return exe, nil
}

// tailLog reads up to the last maxBytes of the file at path and returns it
// as a trimmed string. It is best-effort: on any error (missing file, read
// failure) it returns "". Used to surface the child agent's stderr (written
// to the agent log) in the "agent did not start" error so failures like
// `unknown command "__agent"` are visible to the caller.
func tailLog(path string, maxBytes int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ""
	}
	size := info.Size()
	if size > maxBytes {
		if _, err := f.Seek(size-maxBytes, io.SeekStart); err != nil {
			return ""
		}
	}
	buf, err := io.ReadAll(f)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(buf))
}

// logTailSuffix returns a "\n--- agent log tail ---\n<…>" suffix suitable for
// appending to a spawn-failure error, or "" when the log is empty/unreadable.
func logTailSuffix(path string) string {
	tail := tailLog(path, 2048)
	if tail == "" {
		return ""
	}
	return "\n--- agent log tail ---\n" + tail
}
