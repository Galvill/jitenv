package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/crypto"
)

// TestRunConfigTUICreatesConfigDirOnFreshInstall covers the #190
// regression: on a fresh machine where the user has never run
// jitenv before, $XDG_CONFIG_HOME/jitenv/ does not exist. The
// lockfile.Acquire call in runConfigTUI opens the lock file with
// O_CREATE (but not MkdirAll), so without an explicit MkdirAll
// before the acquire we fail with ENOENT before the TUI's
// loadOrInit "create a new config?" prompt can run.
//
// We stub the TUI binary with a no-op command so runConfigTUI
// completes end-to-end; the assertion is simply that it succeeds
// and that the parent dir is now present.
func TestRunConfigTUICreatesConfigDirOnFreshInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses %LOCALAPPDATA% pathing + a different no-op binary; covered by manual repro")
	}
	// Locate `true` portably: macOS runners ship it at /usr/bin/true,
	// most Linux distros at /bin/true (some at both). exec.LookPath
	// hits PATH, which is reliable across both.
	noop, err := exec.LookPath("true")
	if err != nil {
		t.Skipf("no `true` binary on PATH: %v", err)
	}
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("JITENV_CONFIG", "")
	t.Setenv("JITENV_TUI_BIN", noop)

	saved := configPath
	configPath = ""
	t.Cleanup(func() { configPath = saved })

	cfgDir := filepath.Join(tmp, "jitenv")
	if _, err := os.Stat(cfgDir); !os.IsNotExist(err) {
		t.Fatalf("precondition: %s must not exist, got err=%v", cfgDir, err)
	}

	if err := runConfigTUI(); err != nil {
		t.Fatalf("runConfigTUI failed on fresh install: %v", err)
	}

	st, err := os.Stat(cfgDir)
	if err != nil {
		t.Fatalf("config dir not created: %v", err)
	}
	if !st.IsDir() {
		t.Fatalf("expected dir, got %v", st.Mode())
	}
	if mode := st.Mode().Perm(); mode != 0o700 {
		t.Errorf("config dir mode = %v, want 0700", mode)
	}
}

// runConfigValidate invokes the `config validate` command's RunE against
// the config at cfgPath, returning the command's stdout, stderr, and
// error. Under `go test` stdin/stdout are not TTYs, so crypto.HasTerminal
// reports false and the command takes the no-TTY structural-only path —
// exactly the path a CI caller hits.
func runConfigValidate(t *testing.T, cfgPath string, strict bool) (stdout, stderr string, err error) {
	t.Helper()
	if crypto.HasTerminal() {
		t.Skip("test requires a non-TTY stdin/stdout to exercise the no-TTY path")
	}

	saved := configPath
	configPath = cfgPath
	t.Cleanup(func() { configPath = saved })

	cmd := newConfigValidateCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	args := []string{}
	if strict {
		args = append(args, "--strict")
	}
	cmd.SetArgs(args)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

// TestConfigValidate_NoTTYStrictNote covers the [MED] finding on #255: a
// CI caller who passes --strict with no TTY still gets the structural
// check (exit 0), but must be told the strict check didn't run because
// the passphrase couldn't be prompted and warnings were never computed.
//
//   - Without --strict on the no-TTY path: structural "ok", exit 0, no note.
//   - With --strict on the no-TTY path: structural "ok", exit 0, plus the
//     note on stderr. The exit code is unchanged from the no-TTY default —
//     --strict does NOT hard-fail when the warnings couldn't be evaluated.
func TestConfigValidate_NoTTYStrictNote(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	if err := config.InitNew(cfgPath, []byte("correct horse battery staple")); err != nil {
		t.Fatalf("InitNew: %v", err)
	}

	const note = "--strict ignored: no TTY available"

	t.Run("no-strict has no note", func(t *testing.T) {
		out, errOut, err := runConfigValidate(t, cfgPath, false)
		if err != nil {
			t.Fatalf("validate (no --strict) returned error: %v", err)
		}
		if !strings.Contains(out, "ok") {
			t.Errorf("stdout missing structural %q, got: %q", "ok", out)
		}
		if strings.Contains(errOut, note) {
			t.Errorf("stderr should not contain the strict note without --strict, got: %q", errOut)
		}
	})

	t.Run("strict prints note, exit unchanged", func(t *testing.T) {
		out, errOut, err := runConfigValidate(t, cfgPath, true)
		if err != nil {
			t.Fatalf("validate --strict on no-TTY path must not hard-fail, got error: %v", err)
		}
		if !strings.Contains(out, "ok") {
			t.Errorf("stdout missing structural %q, got: %q", "ok", out)
		}
		if !strings.Contains(errOut, note) {
			t.Errorf("stderr missing strict note %q, got: %q", note, errOut)
		}
	})
}
