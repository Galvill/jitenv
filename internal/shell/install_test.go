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
