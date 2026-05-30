//go:build !windows

package shell_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestHookKeepsWrapDirFirstAfterLaterPrepend regresses #224: Ubuntu's
// stock ~/.profile sources ~/.bashrc (which loads the hook → wrap dir
// prepended) and THEN prepends ~/.local/bin to PATH. The result is
// wrap dir at position 2 with .local/bin at position 1, so any real
// binary in .local/bin (terraform, kubectl, ...) silently masks the
// wrapper symlink and secrets never get injected.
//
// The hook ensures the wrap dir stays first on every PROMPT_COMMAND
// fire via __jitenv_ensure_path. This test simulates one prompt cycle
// after a later prepend and asserts PATH order is restored.
func TestHookKeepsWrapDirFirstAfterLaterPrepend(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	bin := buildBinary(t)
	binDir := strings.TrimSuffix(bin, "/jitenv")

	// Sequence:
	//   1. Source the hook → wrap dir prepended.
	//   2. Mimic Ubuntu .profile: PATH="$HOME/.local/bin:$PATH".
	//   3. Run __jitenv_chpwd (PROMPT_COMMAND fires it before every
	//      prompt) → wrap dir must end up first again.
	script := strings.Join([]string{
		`eval "$(jitenv hook bash)"`,
		`PATH="$HOME/.local/bin:$PATH"`,
		`__jitenv_chpwd`,
		`printf 'FIRST=%s\n' "${PATH%%:*}"`,
		`printf 'WRAP=%s\n' "$__JITENV_WRAP_DIR"`,
	}, "; ")

	cmd := exec.Command("bash", "-c", script)
	cmd.Env = []string{
		"PATH=" + binDir + ":/usr/bin:/bin",
		"HOME=" + t.TempDir(),
		"XDG_RUNTIME_DIR=" + t.TempDir(),
		"TMPDIR=/tmp",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash -c: %v\noutput=%s", err, out)
	}

	var first, wrap string
	for _, ln := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(ln, "FIRST="):
			first = strings.TrimPrefix(ln, "FIRST=")
		case strings.HasPrefix(ln, "WRAP="):
			wrap = strings.TrimPrefix(ln, "WRAP=")
		}
	}
	if wrap == "" {
		t.Fatalf("hook didn't set __JITENV_WRAP_DIR\nfull output: %s", out)
	}
	if first != wrap {
		t.Fatalf("after a later PATH prepend, the wrap dir is not first:\n"+
			"  first PATH entry = %q\n"+
			"  wrap dir         = %q\n"+
			"full output: %s", first, wrap, out)
	}

	// Sanity: the wrap dir should appear EXACTLY once — the
	// ensure_path function should de-dupe rather than accumulate.
	pathCountScript := strings.Join([]string{
		`eval "$(jitenv hook bash)"`,
		`PATH="$HOME/.local/bin:$PATH"`,
		`__jitenv_chpwd`,
		`__jitenv_chpwd`,
		`__jitenv_chpwd`,
		`printf 'COUNT=%s\n' "$(awk -v RS=: -v wrap="$__JITENV_WRAP_DIR" '$0==wrap{c++} END{print c+0}' <<<"$PATH")"`,
	}, "; ")
	cmd2 := exec.Command("bash", "-c", pathCountScript)
	cmd2.Env = cmd.Env
	out2, err := cmd2.CombinedOutput()
	if err != nil {
		t.Fatalf("dedup check: %v\noutput=%s", err, out2)
	}
	if !strings.Contains(string(out2), "COUNT=1\n") {
		t.Errorf("wrap dir should appear exactly once after multiple chpwd calls; output:\n%s", out2)
	}
}
