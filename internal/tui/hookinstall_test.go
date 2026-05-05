package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsHookInstalled_Missing(t *testing.T) {
	dir := t.TempDir()
	rc := filepath.Join(dir, ".bashrc")
	got, err := isHookInstalled(rc, hookLineForShell("bash"))
	if err != nil || got {
		t.Fatalf("expected (false, nil) for missing file, got (%v, %v)", got, err)
	}
}

func TestIsHookInstalled_Present(t *testing.T) {
	dir := t.TempDir()
	rc := filepath.Join(dir, ".bashrc")
	line := hookLineForShell("bash")
	body := "# stuff\nexport PATH=$PATH:/x\n" + line + "\n# more\n"
	if err := os.WriteFile(rc, []byte(body), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := isHookInstalled(rc, line)
	if err != nil || !got {
		t.Fatalf("expected (true, nil), got (%v, %v)", got, err)
	}
}

func TestIsHookInstalled_Absent(t *testing.T) {
	dir := t.TempDir()
	rc := filepath.Join(dir, ".bashrc")
	if err := os.WriteFile(rc, []byte("export PS1='> '\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := isHookInstalled(rc, hookLineForShell("bash"))
	if err != nil || got {
		t.Fatalf("expected (false, nil), got (%v, %v)", got, err)
	}
}

func TestAppendHookLine_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	rc := filepath.Join(dir, ".bashrc")
	line := hookLineForShell("bash")
	if err := appendHookLine(rc, line); err != nil {
		t.Fatalf("append: %v", err)
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

func TestAppendHookLine_AppendsToExisting(t *testing.T) {
	dir := t.TempDir()
	rc := filepath.Join(dir, ".bashrc")
	pre := "export FOO=bar\n"
	if err := os.WriteFile(rc, []byte(pre), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	line := hookLineForShell("bash")
	if err := appendHookLine(rc, line); err != nil {
		t.Fatalf("append: %v", err)
	}
	b, _ := os.ReadFile(rc)
	if !strings.HasPrefix(string(b), pre) {
		t.Fatalf("existing content was not preserved: %q", b)
	}
	if !strings.Contains(string(b), line) {
		t.Fatalf("hook line missing")
	}
}

func TestRcPathForShell(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home: %v", err)
	}
	if got := rcPathForShell("bash"); got != filepath.Join(home, ".bashrc") {
		t.Fatalf("bash rc: %q", got)
	}
	if got := rcPathForShell("zsh"); got != filepath.Join(home, ".zshrc") {
		t.Fatalf("zsh rc: %q", got)
	}
	if got := rcPathForShell("fish"); got != "" {
		t.Fatalf("fish should be unsupported, got %q", got)
	}
}

func TestDetectShell(t *testing.T) {
	t.Setenv("SHELL", "/usr/bin/bash")
	if got := detectShell(); got != "bash" {
		t.Fatalf("bash detection: %q", got)
	}
	t.Setenv("SHELL", "/usr/local/bin/zsh")
	if got := detectShell(); got != "zsh" {
		t.Fatalf("zsh detection: %q", got)
	}
	t.Setenv("SHELL", "/bin/dash")
	if got := detectShell(); got != "" {
		t.Fatalf("unsupported shell should return empty, got %q", got)
	}
	t.Setenv("SHELL", "")
	if got := detectShell(); got != "" {
		t.Fatalf("empty SHELL should return empty, got %q", got)
	}
}
