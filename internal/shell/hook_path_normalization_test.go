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

			script := `eval "$(` + binDir + `/jitenv hook ` + tc.hookCmd + `)"; printf '%s\n' "$__JITENV_WRAP_DIR"`
			cmd := exec.Command(tc.shell, "-c", script)
			cmd.Env = append([]string{"PATH=/usr/bin:/bin", "HOME=/tmp", "XDG_RUNTIME_DIR=" + tc.runtime}, "TMPDIR=/tmp")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s: %v\noutput=%s", tc.shell, err, out)
			}
			got := strings.TrimSpace(string(out))
			if !strings.HasPrefix(got, tc.wantPath) {
				t.Fatalf("expected wrap dir to start with %q (no leading double-slash); got %q", tc.wantPath, got)
			}
			if strings.Contains(got, "//") {
				t.Fatalf("wrap dir contains double slash: %q", got)
			}
		})
	}
}
