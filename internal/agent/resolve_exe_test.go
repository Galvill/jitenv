package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// hookExeName / jitenvExeName mirror the platform-suffix logic in
// resolveAgentExecutable so the test fakes the right filenames.
func hookExeName() string {
	if runtime.GOOS == "windows" {
		return "jitenv-hook.exe"
	}
	return "jitenv-hook"
}

func jitenvExeName() string {
	if runtime.GOOS == "windows" {
		return "jitenv.exe"
	}
	return "jitenv"
}

// withExecutable swaps the package-level os.Executable() result by re-execing
// the test under the faked binary path. Since os.Executable() reads the real
// running binary, we instead validate resolveAgentExecutable's path logic by
// exercising resolveSibling directly via a tiny indirection: we can't easily
// override os.Executable, so we test the resolution rule by faking the dir
// layout and asserting on the candidate-selection behaviour through a copy of
// the running test binary named jitenv-hook.

// TestResolveAgentExecutable_SiblingResolution copies the running test binary
// to a temp dir as `jitenv-hook`, places a sibling `jitenv`, and runs a
// subprocess of the hook copy that calls resolveAgentExecutable and prints the
// result. This exercises the real os.Executable() path.
func TestResolveAgentExecutable_SiblingResolution(t *testing.T) {
	if os.Getenv("JITENV_RESOLVE_HELPER") == "1" {
		// We are the re-execed `jitenv-hook` copy. Run the resolver and
		// print its outcome for the parent to assert on.
		exe, err := resolveAgentExecutable()
		if err != nil {
			os.Stdout.WriteString("ERR:" + err.Error())
			os.Exit(0)
		}
		os.Stdout.WriteString("OK:" + exe)
		os.Exit(0)
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatalf("read self: %v", err)
	}

	t.Run("resolves sibling jitenv", func(t *testing.T) {
		dir := t.TempDir()
		hookPath := filepath.Join(dir, hookExeName())
		jitenvPath := filepath.Join(dir, jitenvExeName())
		if err := os.WriteFile(hookPath, data, 0o755); err != nil {
			t.Fatalf("write hook copy: %v", err)
		}
		// A real (non-empty) sibling jitenv so os.Stat succeeds.
		if err := os.WriteFile(jitenvPath, []byte("jitenv"), 0o755); err != nil {
			t.Fatalf("write sibling jitenv: %v", err)
		}
		out := runHelper(t, hookPath)
		want := "OK:" + jitenvPath
		if out != want {
			t.Fatalf("resolved = %q, want %q", out, want)
		}
	})

	t.Run("errors when sibling jitenv missing", func(t *testing.T) {
		dir := t.TempDir()
		hookPath := filepath.Join(dir, hookExeName())
		if err := os.WriteFile(hookPath, data, 0o755); err != nil {
			t.Fatalf("write hook copy: %v", err)
		}
		out := runHelper(t, hookPath)
		if !strings.HasPrefix(out, "ERR:") {
			t.Fatalf("expected ERR for missing sibling, got %q", out)
		}
		if !strings.Contains(out, "full jitenv binary alongside jitenv-hook") {
			t.Fatalf("error did not mention sibling-jitenv requirement: %q", out)
		}
	})
}

func TestTailLog(t *testing.T) {
	t.Run("missing file returns empty", func(t *testing.T) {
		if got := tailLog(filepath.Join(t.TempDir(), "nope.log"), 2048); got != "" {
			t.Fatalf("tailLog(missing) = %q, want empty", got)
		}
	})

	t.Run("small file returned whole and trimmed", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "agent.log")
		if err := os.WriteFile(p, []byte("  jitenv-hook: unknown command \"__agent\"\n"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		got := tailLog(p, 2048)
		if got != "jitenv-hook: unknown command \"__agent\"" {
			t.Fatalf("tailLog = %q", got)
		}
	})

	t.Run("large file truncated to last maxBytes", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "agent.log")
		body := strings.Repeat("A", 5000) + "TAIL-MARKER"
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		got := tailLog(p, 100)
		if len(got) > 100 {
			t.Fatalf("tailLog len = %d, want <= 100", len(got))
		}
		if !strings.HasSuffix(got, "TAIL-MARKER") {
			t.Fatalf("tailLog did not return the tail: %q", got)
		}
	})

	t.Run("logTailSuffix wraps non-empty tail", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "agent.log")
		if err := os.WriteFile(p, []byte("boom"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		got := logTailSuffix(p)
		if !strings.Contains(got, "--- agent log tail ---") || !strings.HasSuffix(got, "boom") {
			t.Fatalf("logTailSuffix = %q", got)
		}
		if empty := logTailSuffix(filepath.Join(t.TempDir(), "absent.log")); empty != "" {
			t.Fatalf("logTailSuffix(missing) = %q, want empty", empty)
		}
	})
}

// runHelper executes the faked `jitenv-hook` binary (a copy of this test
// binary) so its os.Executable() reports the hook path, with the env flag set
// to take the helper branch in TestResolveAgentExecutable_SiblingResolution.
// It returns the helper's stdout ("OK:<path>" or "ERR:<msg>").
func runHelper(t *testing.T, hookPath string) string {
	t.Helper()
	cmd := exec.Command(hookPath, "-test.run", "TestResolveAgentExecutable_SiblingResolution")
	cmd.Env = append(os.Environ(), "JITENV_RESOLVE_HELPER=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run helper %s: %v (stdout=%q)", hookPath, err, string(out))
	}
	return strings.TrimSpace(string(out))
}
