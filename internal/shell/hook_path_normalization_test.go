package shell_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestHookSnippetStripsTrailingSlash regresses the bash + zsh hook
// snippets against double-slash leakage when XDG_RUNTIME_DIR happens
// to end in a trailing slash. The snippets do raw string concat for
// the wrapper-symlink directory, so an unstripped trailing slash
// pollutes $PATH with `…//jitenv/shells/<pid>/bin`.
func TestHookSnippetStripsTrailingSlash(t *testing.T) {
	cases := []struct {
		name      string
		shell     string
		hookCmd   string
		runtime   string
		wantPath  string
		shellArgs []string
	}{
		{
			name:     "bash trailing slash",
			shell:    "bash",
			hookCmd:  "bash",
			runtime:  "/run/user/1000/",
			wantPath: "/run/user/1000/jitenv/shells/",
		},
		{
			name:     "bash no trailing slash",
			shell:    "bash",
			hookCmd:  "bash",
			runtime:  "/run/user/1000",
			wantPath: "/run/user/1000/jitenv/shells/",
		},
		{
			name:     "zsh trailing slash",
			shell:    "zsh",
			hookCmd:  "zsh",
			runtime:  "/run/user/1000/",
			wantPath: "/run/user/1000/jitenv/shells/",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.shell); err != nil {
				t.Skipf("%s not available", tc.shell)
			}
			bin := buildBinary(t)
			binDir := strings.TrimSuffix(bin, "/jitenv")

			// The hook calls `jitenv __chpwd` once at load time via a
			// bare name lookup, so binDir must be on $PATH in addition
			// to the test's restricted defaults — otherwise we get a
			// `command not found` line ahead of the wrap-dir output.
			// HOME is set to the temp dir so the chpwd helper's config
			// lookup ($XDG_CONFIG_HOME default) doesn't touch the
			// developer's real config.
			script := `eval "$(jitenv hook ` + tc.hookCmd + `)"; printf '%s\n' "$__JITENV_WRAP_DIR"`
			cmd := exec.Command(tc.shell, "-c", script)
			cmd.Env = []string{
				"PATH=" + binDir + ":/usr/bin:/bin",
				"HOME=" + t.TempDir(),
				"XDG_RUNTIME_DIR=" + tc.runtime,
				"TMPDIR=/tmp",
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s: %v\noutput=%s", tc.shell, err, out)
			}
			// Take the last line — diagnostic output from the hook's
			// load-time chpwd may print before the wrap-dir line we
			// care about.
			got := strings.TrimSpace(string(out))
			lastLine := got
			if i := strings.LastIndex(got, "\n"); i >= 0 {
				lastLine = got[i+1:]
			}
			if !strings.HasPrefix(lastLine, tc.wantPath) {
				t.Fatalf("expected wrap dir to start with %q (no leading double-slash); got %q\nfull output: %q", tc.wantPath, lastLine, got)
			}
			if strings.Contains(lastLine, "//") {
				t.Fatalf("wrap dir contains double slash: %q", lastLine)
			}
		})
	}
}
