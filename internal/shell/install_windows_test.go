//go:build windows

package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDetectShellWindows pins the Windows branch of DetectShell. $SHELL
// is normally unset on Windows; we return "powershell" unconditionally
// (cmd.exe and PowerShell 5.x are unsupported — see #39).
func TestDetectShellWindows(t *testing.T) {
	t.Setenv("SHELL", "")
	if got := DetectShell(); got != "powershell" {
		t.Errorf("DetectShell on Windows with empty SHELL: got %q want %q", got, "powershell")
	}
	// Even if a bash-style $SHELL leaks in (e.g. inside a Git Bash
	// terminal that happens to run the Windows build), we still
	// return powershell — the runtime port only supports pwsh.
	t.Setenv("SHELL", "/usr/bin/bash")
	if got := DetectShell(); got != "powershell" {
		t.Errorf("DetectShell on Windows with bash SHELL: got %q want %q", got, "powershell")
	}
}

// TestRcPathPowerShellDefaultsToDocuments asserts the fallback path
// when pwsh is not on PATH. The Windows default is
// %USERPROFILE%\Documents\PowerShell\Microsoft.PowerShell_profile.ps1
// (pwsh 7+, no "WindowsPowerShell" 5.x suffix).
func TestRcPathPowerShellDefaultsToDocuments(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	// Point PATH at an empty dir so queryPwshProfilePath misses and we
	// exercise the documented fallback.
	t.Setenv("PATH", t.TempDir())

	got := RcPath("powershell")
	want := filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
	if got != want {
		t.Errorf("RcPath(powershell) fallback: got %q want %q", got, want)
	}
	if got := RcPath("pwsh"); got != want {
		t.Errorf("RcPath(pwsh) alias: got %q want %q", got, want)
	}
}

// TestInstallShellPowerShellWindows exercises the end-to-end install
// path against a tempdir-rooted $USERPROFILE. The hook line should land
// in $PROFILE, the login-chain fields should stay zero, and a second
// install should be a no-op.
func TestInstallShellPowerShellWindows(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir()) // force fallback

	rep, err := InstallShell("powershell")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	wantRc := filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
	if rep.RcPath != wantRc {
		t.Errorf("RcPath: got %q want %q", rep.RcPath, wantRc)
	}
	if !rep.RcAdded {
		t.Errorf("expected RcAdded=true on first install")
	}
	if rep.LoginPath != "" || rep.LoginAdded {
		t.Errorf("pwsh install should not touch a login file: %+v", rep)
	}
	body, err := os.ReadFile(rep.RcPath)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	if !strings.Contains(string(body), HookLine("powershell")) {
		t.Errorf("hook line missing from profile:\n%s", body)
	}
	// Idempotent.
	rep2, err := InstallShell("powershell")
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if rep2.RcAdded {
		t.Errorf("second install should be no-op")
	}
	body, _ = os.ReadFile(rep.RcPath)
	if strings.Count(string(body), HookLine("powershell")) != 1 {
		t.Errorf("hook line should appear exactly once: %q", body)
	}
}

// TestCurrentStatusPowerShellWindows asserts the status command's view
// of the install state. PowerShell has no login-chain split, so
// LoginPath stays empty and LoginSources defaults to true.
func TestCurrentStatusPowerShellWindows(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())

	// Pre-install so Installed=true.
	if _, err := InstallShell("powershell"); err != nil {
		t.Fatalf("install: %v", err)
	}
	st, err := CurrentStatus()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.Shell != "powershell" {
		t.Errorf("Shell: got %q want %q", st.Shell, "powershell")
	}
	if !st.Installed {
		t.Errorf("Installed: expected true after InstallShell")
	}
	if st.LoginPath != "" {
		t.Errorf("LoginPath: expected empty for powershell, got %q", st.LoginPath)
	}
	if !st.LoginSources {
		t.Errorf("LoginSources: expected true (no separate login file)")
	}
}
