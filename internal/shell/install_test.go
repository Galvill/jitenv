//go:build !windows

package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsInstalled_Missing(t *testing.T) {
	dir := t.TempDir()
	rc := filepath.Join(dir, ".bashrc")
	got, err := IsInstalled(rc, HookLine("bash"))
	if err != nil || got {
		t.Fatalf("expected (false, nil) for missing file, got (%v, %v)", got, err)
	}
}

func TestIsInstalled_Present(t *testing.T) {
	dir := t.TempDir()
	rc := filepath.Join(dir, ".bashrc")
	line := HookLine("bash")
	body := "# stuff\nexport PATH=$PATH:/x\n" + line + "\n# more\n"
	if err := os.WriteFile(rc, []byte(body), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := IsInstalled(rc, line)
	if err != nil || !got {
		t.Fatalf("expected (true, nil), got (%v, %v)", got, err)
	}
}

func TestIsInstalled_Absent(t *testing.T) {
	dir := t.TempDir()
	rc := filepath.Join(dir, ".bashrc")
	if err := os.WriteFile(rc, []byte("export PS1='> '\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := IsInstalled(rc, HookLine("bash"))
	if err != nil || got {
		t.Fatalf("expected (false, nil), got (%v, %v)", got, err)
	}
}

func TestInstall_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	rc := filepath.Join(dir, ".bashrc")
	line := HookLine("bash")
	added, err := Install(rc, line)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !added {
		t.Errorf("expected added=true on first install")
	}
	b, err := os.ReadFile(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(b), line) {
		t.Fatalf("expected hook line in file:\n%s", b)
	}
	if !strings.Contains(string(b), "# jitenv:") {
		t.Fatalf("expected comment header in file")
	}
}

func TestInstall_Idempotent(t *testing.T) {
	dir := t.TempDir()
	rc := filepath.Join(dir, ".bashrc")
	line := HookLine("bash")
	if _, err := Install(rc, line); err != nil {
		t.Fatalf("first install: %v", err)
	}
	added, err := Install(rc, line)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if added {
		t.Errorf("second install should report added=false")
	}
	b, _ := os.ReadFile(rc)
	if strings.Count(string(b), line) != 1 {
		t.Fatalf("hook line should appear exactly once:\n%s", b)
	}
}

func TestInstall_AppendsToExisting(t *testing.T) {
	dir := t.TempDir()
	rc := filepath.Join(dir, ".bashrc")
	pre := "export FOO=bar\n"
	if err := os.WriteFile(rc, []byte(pre), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	line := HookLine("bash")
	added, err := Install(rc, line)
	if err != nil || !added {
		t.Fatalf("install: added=%v err=%v", added, err)
	}
	b, _ := os.ReadFile(rc)
	if !strings.HasPrefix(string(b), pre) {
		t.Fatalf("existing content was not preserved: %q", b)
	}
	if !strings.Contains(string(b), line) {
		t.Fatalf("hook line missing")
	}
}

func TestRcPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home: %v", err)
	}
	if got := RcPath("bash"); got != filepath.Join(home, ".bashrc") {
		t.Fatalf("bash rc: %q", got)
	}
	if got := RcPath("zsh"); got != filepath.Join(home, ".zshrc") {
		t.Fatalf("zsh rc: %q", got)
	}
	if got := RcPath("fish"); got != "" {
		t.Fatalf("fish should be unsupported, got %q", got)
	}
}

// withFakeHome replaces $HOME for the duration of a test so the
// installer touches a sandbox instead of the user's real dotfiles.
func withFakeHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

func TestInstallShell_BashCreatesLoginChain(t *testing.T) {
	home := withFakeHome(t)
	rep, err := InstallShell("bash")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !rep.RcAdded {
		t.Errorf("expected RcAdded=true on first install")
	}
	if rep.LoginPath != filepath.Join(home, ".bash_profile") {
		t.Errorf("expected new .bash_profile, got %q", rep.LoginPath)
	}
	if !rep.LoginAdded {
		t.Errorf("expected LoginAdded=true when no login file existed")
	}
	body, _ := os.ReadFile(rep.LoginPath)
	if !strings.Contains(string(body), "/.bashrc") {
		t.Errorf("created .bash_profile should source ~/.bashrc; got:\n%s", body)
	}
}

func TestInstallShell_BashAppendsToExistingProfile(t *testing.T) {
	home := withFakeHome(t)
	profile := filepath.Join(home, ".profile")
	if err := os.WriteFile(profile, []byte("export PS1='> '\n"), 0644); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	rep, err := InstallShell("bash")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if rep.LoginPath != profile {
		t.Errorf("expected modification of existing .profile, got %q", rep.LoginPath)
	}
	if !rep.LoginAdded {
		t.Errorf("expected LoginAdded=true on .profile that didn't source bashrc")
	}
	body, _ := os.ReadFile(profile)
	if !strings.Contains(string(body), "BASH_VERSION") {
		t.Errorf(".profile additions should be bash-guarded; got:\n%s", body)
	}
	if !strings.HasPrefix(string(body), "export PS1") {
		t.Errorf("existing .profile content was not preserved: %q", body)
	}
}

func TestInstallShell_BashSkipsWhenAlreadySources(t *testing.T) {
	home := withFakeHome(t)
	bashProfile := filepath.Join(home, ".bash_profile")
	if err := os.WriteFile(bashProfile,
		[]byte("[[ -f ~/.bashrc ]] && source ~/.bashrc\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rep, err := InstallShell("bash")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if rep.LoginAdded {
		t.Errorf("expected no login-file change when it already sources ~/.bashrc")
	}
	if !rep.LoginAlreadyOK {
		t.Errorf("expected LoginAlreadyOK=true")
	}
	body, _ := os.ReadFile(bashProfile)
	if strings.Count(string(body), "source ~/.bashrc") != 1 {
		t.Errorf("source line should appear exactly once: %q", body)
	}
}

func TestInstallShell_ZshOnlyTouchesZshrc(t *testing.T) {
	home := withFakeHome(t)
	rep, err := InstallShell("zsh")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if rep.RcPath != filepath.Join(home, ".zshrc") {
		t.Errorf("zsh rc path: %q", rep.RcPath)
	}
	if rep.LoginPath != "" || rep.LoginAdded {
		t.Errorf("zsh install should not touch a login file (got %+v)", rep)
	}
}

func TestCurrentStatus_BashReportsLoginGap(t *testing.T) {
	home := withFakeHome(t)
	t.Setenv("SHELL", "/bin/bash")

	if err := os.WriteFile(filepath.Join(home, ".bashrc"),
		[]byte(HookLine("bash")+"\n"), 0644); err != nil {
		t.Fatalf("seed bashrc: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".bash_profile"),
		[]byte("# unrelated\n"), 0644); err != nil {
		t.Fatalf("seed bash_profile: %v", err)
	}
	st, err := CurrentStatus()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !st.Installed {
		t.Errorf("expected Installed=true")
	}
	if st.LoginPath != filepath.Join(home, ".bash_profile") {
		t.Errorf("LoginPath: %q", st.LoginPath)
	}
	if st.LoginSources {
		t.Errorf("expected LoginSources=false when .bash_profile lacks a source line")
	}
}

func TestDetectShell(t *testing.T) {
	t.Setenv("SHELL", "/usr/bin/bash")
	if got := DetectShell(); got != "bash" {
		t.Fatalf("bash detection: %q", got)
	}
	t.Setenv("SHELL", "/usr/local/bin/zsh")
	if got := DetectShell(); got != "zsh" {
		t.Fatalf("zsh detection: %q", got)
	}
	t.Setenv("SHELL", "/bin/dash")
	if got := DetectShell(); got != "" {
		t.Fatalf("unsupported shell should return empty, got %q", got)
	}
	t.Setenv("SHELL", "")
	if got := DetectShell(); got != "" {
		t.Fatalf("empty SHELL should return empty, got %q", got)
	}
}
