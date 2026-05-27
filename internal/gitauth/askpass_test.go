package gitauth

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAskpass_Username(t *testing.T) {
	t.Setenv(JitenvGitTokenEnv, "should-not-appear")
	var buf bytes.Buffer
	if err := Askpass("Username for 'https://github.com':", &buf); err != nil {
		t.Fatalf("Askpass: %v", err)
	}
	if got, want := strings.TrimRight(buf.String(), "\r\n"), GitUsernameForToken; got != want {
		t.Errorf("Username response: got %q want %q", got, want)
	}
}

func TestAskpass_Password(t *testing.T) {
	t.Setenv(JitenvGitTokenEnv, "ghp_TESTTOKEN")
	var buf bytes.Buffer
	if err := Askpass("Password for 'https://oauth2@github.com':", &buf); err != nil {
		t.Fatalf("Askpass: %v", err)
	}
	if got, want := strings.TrimRight(buf.String(), "\r\n"), "ghp_TESTTOKEN"; got != want {
		t.Errorf("Password response: got %q want %q", got, want)
	}
}

func TestAskpass_UnknownPromptStaysSilent(t *testing.T) {
	t.Setenv(JitenvGitTokenEnv, "should-not-leak-on-unknown-prompt")
	var buf bytes.Buffer
	if err := Askpass("Two-factor code:", &buf); err != nil {
		t.Fatalf("Askpass: %v", err)
	}
	if got := strings.TrimRight(buf.String(), "\r\n"); got != "" {
		t.Errorf("Unknown prompt response: got %q, want empty (token must not leak to non-password prompts)", got)
	}
}

func TestAskpass_PasswordWithEmptyEnv(t *testing.T) {
	// Explicitly unset — Go's t.Setenv with empty string still sets
	// the var to "". Use os.Unsetenv directly + restore.
	old, had := os.LookupEnv(JitenvGitTokenEnv)
	os.Unsetenv(JitenvGitTokenEnv)
	t.Cleanup(func() {
		if had {
			os.Setenv(JitenvGitTokenEnv, old)
		}
	})

	var buf bytes.Buffer
	if err := Askpass("Password:", &buf); err != nil {
		t.Fatalf("Askpass: %v", err)
	}
	if got := strings.TrimRight(buf.String(), "\r\n"); got != "" {
		t.Errorf("Password with no env: got %q, want empty", got)
	}
}

func TestFirstWord(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Username for 'https://github.com':", "Username"},
		{"Password:", "Password"},
		{"  Password   ", "Password"},
		{"", ""},
		{"Just-one-token:", "Just-one-token"},
	}
	for _, c := range cases {
		if got := firstWord(c.in); got != c.want {
			t.Errorf("firstWord(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEnsureShim_CreatesAndIsExecutable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	t.Setenv("LOCALAPPDATA", dir)

	exe := "/usr/local/bin/jitenv"
	if runtime.GOOS == "windows" {
		exe = `C:\Program Files\jitenv\jitenv.exe`
	}
	path, err := EnsureShim(exe)
	if err != nil {
		t.Fatalf("EnsureShim: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(got), exe) {
		t.Errorf("shim body missing jitenv path %q; got:\n%s", exe, got)
	}
	if !strings.Contains(string(got), "__git_askpass") {
		t.Errorf("shim body missing __git_askpass subcommand; got:\n%s", got)
	}

	// On Unix, the shim must be 0700 (executable for owner only).
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Errorf("shim perm: got %o, want 0700", info.Mode().Perm())
		}
	}

	// Second call with the same jitenvExe should be a no-op (no
	// mtime bump). Read the file to confirm contents unchanged.
	info1, _ := os.Stat(path)
	if _, err := EnsureShim(exe); err != nil {
		t.Fatalf("second EnsureShim: %v", err)
	}
	info2, _ := os.Stat(path)
	if info1.ModTime() != info2.ModTime() {
		t.Errorf("EnsureShim should be a no-op when contents match (mtime changed: %v → %v)", info1.ModTime(), info2.ModTime())
	}
}

func TestEnsureShim_RewritesOnPathChange(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	t.Setenv("LOCALAPPDATA", dir)

	if _, err := EnsureShim("/old/jitenv"); err != nil {
		t.Fatalf("first EnsureShim: %v", err)
	}
	if _, err := EnsureShim("/new/jitenv"); err != nil {
		t.Fatalf("second EnsureShim: %v", err)
	}
	path, _ := ShimPath()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "/new/jitenv") {
		t.Errorf("shim wasn't rewritten on path change; got:\n%s", got)
	}
	if strings.Contains(string(got), "/old/jitenv") {
		t.Errorf("shim still contains stale path; got:\n%s", got)
	}

	// Sanity: rebuild ShimPath and confirm it lives under the test
	// temp dir we set XDG_DATA_HOME to (no escape).
	if !strings.HasPrefix(path, dir) {
		t.Errorf("shim path %q not under XDG_DATA_HOME %q", path, dir)
	}
	// Sanity: the parent dir must exist.
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Errorf("shim parent dir missing: %v", err)
	}
}
