//go:build !windows

package shim_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// buildBinary compiles the jitenv binary into the test's temp dir so
// we can drop wrapper symlinks pointing at it. Mirrors the helper in
// internal/run and internal/shell.
func buildBinary(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "jitenv")
	cmd := exec.Command("go", "build", "-o", out, "../../cmd/jitenv")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}
	return out
}

// TestShimSuppressesWarningWithMarker is the regression test for
// issue #71: a `cwd_glob` mapping that lists both a command and its
// interpreter (e.g. npm + node) used to render the agent-down
// countdown twice because os.Getppid() is unchanged across the
// execve chain. The fix propagates __JITENV_AGENT_WARNED=1 after the
// first warning so subsequent shim entries short-circuit.
//
// We can't fake an execve chain inside the test runner cheaply, so
// we exercise the suppression directly: invoke the shim symlink
// twice. The first call (no marker, agent down) must print the
// warning; the second call (marker set, agent still down) must NOT.
// Both calls must exec the real binary either way.
func TestShimSuppressesWarningWithMarker(t *testing.T) {
	bin := buildBinary(t)

	dir := t.TempDir()

	// Real "fakecmd" the wrapper should execve into. Print a stable
	// token so we can confirm the exec completed in each call.
	fakeBin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	realCmd := filepath.Join(fakeBin, "fakecmd")
	if err := os.WriteFile(realCmd, []byte("#!/bin/sh\necho RAN\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Wrapper symlink "fakecmd" -> jitenv. main.go dispatches into
	// shim.Main because filepath.Base(argv[0]) != "jitenv".
	wrapDir := filepath.Join(dir, "wrap")
	if err := os.MkdirAll(wrapDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(wrapDir, "fakecmd")
	if err := os.Symlink(bin, wrapper); err != nil {
		t.Fatal(err)
	}

	// Empty runtime dir → no agent socket → shim hits the warn path.
	runtimeDir := filepath.Join(dir, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// PATH puts the real fakecmd dir on the search path so the shim's
	// lookPathExcluding finds it after skipping wrapDir.
	pathEnv := fakeBin + string(os.PathListSeparator) + os.Getenv("PATH")

	// The shim's shouldInject() requires ppid (or pid) to equal
	// __JITENV_SHELL_PID. The exec.Command child has ppid = this test
	// process's pid, so set the marker to that.
	shellPID := strconv.Itoa(os.Getpid())

	baseEnv := []string{
		"PATH=" + pathEnv,
		"XDG_RUNTIME_DIR=" + runtimeDir,
		"__JITENV_WRAP_DIR=" + wrapDir,
		"__JITENV_SHELL_PID=" + shellPID,
		"JITENV_HOOK_DELAY=0",
	}
	// Inherit HOME so the binary's startup doesn't choke on a missing
	// home dir on hermetic CI runners.
	if home := os.Getenv("HOME"); home != "" {
		baseEnv = append(baseEnv, "HOME="+home)
	}

	run := func(env []string) (string, error) {
		cmd := exec.Command(wrapper)
		cmd.Env = env
		// Detach stdin from a TTY (pipe by default) so WarnAndWait
		// takes the non-TTY fast path — no spinning on the countdown
		// in tests; see issue #64.
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		err := cmd.Run()
		return buf.String(), err
	}

	// 1. No marker → warning expected.
	out1, err := run(baseEnv)
	if err != nil {
		t.Fatalf("first run: %v\noutput=%s", err, out1)
	}
	if !strings.Contains(out1, "agent is not loaded") {
		t.Errorf("first run: expected 'agent is not loaded' warning;\noutput=%s", out1)
	}
	if !strings.Contains(out1, "RAN") {
		t.Errorf("first run: expected fakecmd to exec ('RAN');\noutput=%s", out1)
	}

	// 2. Marker set → no warning, but the real binary still runs.
	out2, err := run(append(baseEnv, "__JITENV_AGENT_WARNED=1"))
	if err != nil {
		t.Fatalf("second run: %v\noutput=%s", err, out2)
	}
	if strings.Contains(out2, "agent is not loaded") {
		t.Errorf("second run: warning fired despite __JITENV_AGENT_WARNED=1;\noutput=%s", out2)
	}
	if !strings.Contains(out2, "RAN") {
		t.Errorf("second run: expected fakecmd to exec ('RAN');\noutput=%s", out2)
	}
}

// TestShimSuppressesInjectionWithMarker is the regression test for
// issue #77: when a cwd_glob mapping lists both a command and its
// interpreter (e.g. npm + node), env vars used to be fetched and
// appended twice because os.Getppid() doesn't change across execve.
// The fix propagates __JITENV_INJECTED=1 after the first successful
// fetch so subsequent shim entries short-circuit — no fetch attempt,
// no notice, no warn — just execReal transparently.
//
// We don't fake an execve chain inside the test runner; instead we
// invoke the shim twice and assert that the second call (marker set)
// produces no warning, no notice, and still execs the real binary.
// The agent is intentionally down here (empty runtime dir): call 1
// would normally hit the agent-down warn path, call 2 with the
// marker must bypass *everything* — including the warn — and just
// pass through.
func TestShimSuppressesInjectionWithMarker(t *testing.T) {
	bin := buildBinary(t)

	dir := t.TempDir()

	fakeBin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	realCmd := filepath.Join(fakeBin, "fakecmd")
	// Print the env we received so we can assert the marker propagated
	// through execve and that no notice/warning was printed on call 2.
	if err := os.WriteFile(realCmd, []byte("#!/bin/sh\necho RAN\nenv\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	wrapDir := filepath.Join(dir, "wrap")
	if err := os.MkdirAll(wrapDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(wrapDir, "fakecmd")
	if err := os.Symlink(bin, wrapper); err != nil {
		t.Fatal(err)
	}

	runtimeDir := filepath.Join(dir, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	pathEnv := fakeBin + string(os.PathListSeparator) + os.Getenv("PATH")
	shellPID := strconv.Itoa(os.Getpid())

	baseEnv := []string{
		"PATH=" + pathEnv,
		"XDG_RUNTIME_DIR=" + runtimeDir,
		"__JITENV_WRAP_DIR=" + wrapDir,
		"__JITENV_SHELL_PID=" + shellPID,
		"JITENV_HOOK_DELAY=0",
	}
	if home := os.Getenv("HOME"); home != "" {
		baseEnv = append(baseEnv, "HOME="+home)
	}

	run := func(env []string) (string, error) {
		cmd := exec.Command(wrapper)
		cmd.Env = env
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		err := cmd.Run()
		return buf.String(), err
	}

	// Marker set on call AND matches the session nonce (security #120
	// requires both — the shell hook sets nonce at load time): shim
	// must short-circuit. No warning even though the agent is down;
	// the real binary still runs.
	const nonce = "feedfacecafebeef"
	out, err := run(append(baseEnv,
		"__JITENV_INJECTED="+nonce,
		"__JITENV_SESSION_NONCE="+nonce,
	))
	if err != nil {
		t.Fatalf("run: %v\noutput=%s", err, out)
	}
	if strings.Contains(out, "agent is not loaded") {
		t.Errorf("warning fired despite matching marker; output=%s", out)
	}
	if strings.Contains(out, "jitenv: injected") {
		t.Errorf("notice fired despite matching marker; output=%s", out)
	}
	if !strings.Contains(out, "RAN") {
		t.Errorf("expected fakecmd to exec ('RAN');\noutput=%s", out)
	}
	if !strings.Contains(out, "__JITENV_INJECTED="+nonce) {
		t.Errorf("expected __JITENV_INJECTED=%s to propagate to child env;\noutput=%s", nonce, out)
	}
}

// TestShimRecoversWrapDirFromPath is the regression for the second
// half of #182: when turbo's strict env mode strips __JITENV_WRAP_DIR
// AND the wrapper is invoked via execvp-by-name (argv[0]="npm", not
// the full path), the shim's old fallback `filepath.Dir(argv[0])`
// produces "." — which broke both the PATH-exclusion (infinite
// re-exec loop) and the marker-file bypass added by the first
// half of the fix. Recovery scans PATH for an entry strictly under
// the agent's runtime dir <runtime>/shells/<pid>/bin.
//
// Simulates the turbo-worker environment by:
//   - placing the wrapper under a runtime / shells/<pid>/ bin layout
//     (so the recovery scan recognises the shape);
//   - setting XDG_RUNTIME_DIR so agent.DefaultPaths().ShellsDir
//     resolves to the test's runtime tree;
//   - dropping the injection marker file in <shellDir>/injected;
//   - launching the wrapper with argv[0]="fakecmd" (basename only —
//     mirrors how execvp("npm", argv) passes argv[0]) and env
//     stripped of __JITENV_WRAP_DIR.
//
// The shim must recover the wrap dir via the PATH scan, find the
// marker, and short-circuit.
func TestShimRecoversWrapDirFromPath(t *testing.T) {
	bin := buildBinary(t)

	dir := t.TempDir()

	fakeBin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	realCmd := filepath.Join(fakeBin, "fakecmd")
	if err := os.WriteFile(realCmd, []byte("#!/bin/sh\necho RAN\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// XDG_RUNTIME_DIR is the root agent.DefaultPaths() resolves
	// against; the recovery scan walks PATH for entries strictly
	// under <runtimeDir>/jitenv/shells/, so build the per-shell
	// tree at that location.
	runtimeDir := filepath.Join(dir, "runtime")
	shellsDir := filepath.Join(runtimeDir, "jitenv", "shells")
	shellDir := filepath.Join(shellsDir, "12345") // synthetic pid
	wrapDir := filepath.Join(shellDir, "bin")
	if err := os.MkdirAll(wrapDir, 0o700); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(wrapDir, "fakecmd")
	if err := os.Symlink(bin, wrapper); err != nil {
		t.Fatal(err)
	}

	// Marker content must be the child's pgid for markerFileSays to
	// honour it (pgid-based scoping fixes the "stale marker bypasses
	// next command" bug from #182).
	if err := os.WriteFile(filepath.Join(shellDir, "injected"), []byte("any-content"), 0o600); err != nil {
		t.Fatal(err)
	}

	// PATH: wrap dir first (so PATH-lookup of "fakecmd" finds the
	// wrapper), then the real-cmd dir (so lookPathExcluding finds
	// the real one once the wrap dir is correctly excluded).
	pathEnv := wrapDir + string(os.PathListSeparator) + fakeBin

	env := []string{
		"PATH=" + pathEnv,
		"XDG_RUNTIME_DIR=" + runtimeDir,
		"JITENV_HOOK_DELAY=0",
	}
	if home := os.Getenv("HOME"); home != "" {
		env = append(env, "HOME="+home)
	}

	// argv[0]="fakecmd" (basename, not wrapper path) — mirrors how
	// execvp("npm", argv) leaves argv[0] in a turbo-spawned worker.
	cmd := exec.Command(wrapper)
	cmd.Args = []string{"fakecmd"}
	cmd.Env = env
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\noutput=%s", err, buf.String())
	}
	out := buf.String()

	if strings.Contains(out, "agent is not loaded") {
		t.Errorf("warning fired despite recoverable wrap dir + marker file; output=%s", out)
	}
	if strings.Contains(out, "jitenv: injected") {
		t.Errorf("notice fired despite recoverable wrap dir + marker file; output=%s", out)
	}
	if !strings.Contains(out, "RAN") {
		t.Errorf("expected fakecmd to exec ('RAN');\noutput=%s", out)
	}
}

// TestShimSuppressesInjectionViaMarkerFile is the regression test for
// the env-stripping branch of issue #182: turbo strict env mode (and
// firejail / bwrap / sandboxer variants) strips __JITENV_INJECTED
// and __JITENV_SESSION_NONCE before spawning children, so the env-
// based bypass in TestShimSuppressesInjectionWithMarker can't fire.
// The fallback is an on-disk marker file at <wrap-dir>/../injected.
// This test simulates that case directly: drop the marker file by
// hand (no first-pass run to create it — we want to exercise the
// file-only branch), then invoke the shim with NO env markers set
// and confirm the bypass still fires.
func TestShimSuppressesInjectionViaMarkerFile(t *testing.T) {
	bin := buildBinary(t)

	dir := t.TempDir()

	fakeBin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	realCmd := filepath.Join(fakeBin, "fakecmd")
	if err := os.WriteFile(realCmd, []byte("#!/bin/sh\necho RAN\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Wrap-dir layout MUST be <shellDir>/bin (the shim's
	// shellDirFromWrap derives shellDir as filepath.Dir(wrapDir) and
	// rejects wrap-dirs whose basename isn't "bin").
	shellDir := filepath.Join(dir, "shell-xxx")
	wrapDir := filepath.Join(shellDir, "bin")
	if err := os.MkdirAll(wrapDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(wrapDir, "fakecmd")
	if err := os.Symlink(bin, wrapper); err != nil {
		t.Fatal(err)
	}

	// Marker content is the chain's process-group id. Children
	// spawned by exec.Command inherit this test's pgid, so writing
	// our pgid here makes the bypass fire under the child.
	if err := os.WriteFile(filepath.Join(shellDir, "injected"), []byte("any-content"), 0o600); err != nil {
		t.Fatal(err)
	}

	runtimeDir := filepath.Join(dir, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	pathEnv := fakeBin + string(os.PathListSeparator) + os.Getenv("PATH")

	// Crucially: NO __JITENV_INJECTED, NO __JITENV_SESSION_NONCE, NO
	// __JITENV_SHELL_PID — simulates turbo's strict-env stripping of
	// jitenv-namespaced vars. __JITENV_WRAP_DIR is also dropped; the
	// shim's fallback resolves the wrap dir from argv[0].
	env := []string{
		"PATH=" + pathEnv,
		"XDG_RUNTIME_DIR=" + runtimeDir,
		"JITENV_HOOK_DELAY=0",
	}
	if home := os.Getenv("HOME"); home != "" {
		env = append(env, "HOME="+home)
	}

	cmd := exec.Command(wrapper)
	cmd.Env = env
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\noutput=%s", err, buf.String())
	}
	out := buf.String()

	if strings.Contains(out, "agent is not loaded") {
		t.Errorf("warning fired despite marker file; output=%s", out)
	}
	if strings.Contains(out, "jitenv: injected") {
		t.Errorf("notice fired despite marker file; output=%s", out)
	}
	if !strings.Contains(out, "RAN") {
		t.Errorf("expected fakecmd to exec ('RAN');\noutput=%s", out)
	}
}

// TestShimWarnPathWritesMarkerFile covers the second symptom of
// #182: when the agent is locked, the first shim in a command tree
// hits the warn path (agent-down countdown), the user dismisses it,
// and the command runs without env injection. Without the on-disk
// marker, turbo workers strip __JITENV_AGENT_WARNED and re-prompt
// the countdown per worker (real user bug). This test asserts the
// shim drops the marker file on the warn path too, so downstream
// shim hops bypass via markerFileSays() even when env is stripped.
//
// We can't drive the interactive countdown from a test, so we
// detach stdin from a TTY (cmd.Stdin = nil — the agentwarn.WarnAndWait
// non-TTY fast path runs and returns immediately). That path is the
// CI / non-interactive flow but exercises the same warn-marker
// write code below it.
func TestShimWarnPathWritesMarkerFile(t *testing.T) {
	bin := buildBinary(t)

	dir := t.TempDir()

	fakeBin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	realCmd := filepath.Join(fakeBin, "fakecmd")
	if err := os.WriteFile(realCmd, []byte("#!/bin/sh\necho RAN\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// <shellDir>/bin layout so shellDirFromWrap accepts it.
	shellDir := filepath.Join(dir, "shell-xxx")
	wrapDir := filepath.Join(shellDir, "bin")
	if err := os.MkdirAll(wrapDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(wrapDir, "fakecmd")
	if err := os.Symlink(bin, wrapper); err != nil {
		t.Fatal(err)
	}
	// Empty runtime dir → no agent socket → shim hits the warn path.
	runtimeDir := filepath.Join(dir, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	pathEnv := fakeBin + string(os.PathListSeparator) + os.Getenv("PATH")
	shellPID := strconv.Itoa(os.Getpid())

	env := []string{
		"PATH=" + pathEnv,
		"XDG_RUNTIME_DIR=" + runtimeDir,
		"__JITENV_WRAP_DIR=" + wrapDir,
		"__JITENV_SHELL_PID=" + shellPID,
		"JITENV_HOOK_DELAY=0",
	}
	if home := os.Getenv("HOME"); home != "" {
		env = append(env, "HOME="+home)
	}

	cmd := exec.Command(wrapper)
	cmd.Env = env
	// Non-TTY stdin = agentwarn's fast path returns immediately
	// (it prints the warn and continues without the countdown).
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\noutput=%s", err, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "agent is not loaded") {
		t.Errorf("expected agent-down warning;\noutput=%s", out)
	}
	if !strings.Contains(out, "RAN") {
		t.Errorf("expected fakecmd to exec ('RAN');\noutput=%s", out)
	}

	// The fix: marker file is present after a warn-path traversal so
	// downstream re-wrapped commands (turbo workers) bypass instead of
	// re-prompting the countdown.
	markerPath := filepath.Join(shellDir, "injected")
	if _, err := os.Stat(markerPath); err != nil {
		t.Errorf("expected marker file at %s after warn-path; stat err=%v;\noutput=%s",
			markerPath, err, out)
	}
}
