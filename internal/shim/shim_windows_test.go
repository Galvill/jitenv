//go:build windows

package shim

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestMain is the entry point used to re-exec ourselves as a subprocess
// that exercises execReal directly. The Windows execReal calls os.Exit
// on success (there is no exec-replace on Windows), so it cannot be
// tested in-process. The subprocess pattern below — go test re-exec
// with a magic env var — is the standard Go workaround.
//
// When JITENV_TEST_EXEC_REAL_TARGET is set, the test binary acts as a
// driver: it builds nothing, just calls execReal with the target path
// and any extra args, propagating the child's exit code via os.Exit.
// When the env var is unset, the test binary behaves normally and runs
// the test functions.
func TestMain(m *testing.M) {
	if target := os.Getenv("JITENV_TEST_EXEC_REAL_TARGET"); target != "" {
		args := []string{filepath.Base(target)}
		args = append(args, os.Args[1:]...)
		// execReal calls os.Exit; this never returns.
		if err := execReal(target, args, os.Environ()); err != nil {
			// Failed to start; surface and exit non-zero.
			_, _ = os.Stderr.WriteString(err.Error() + "\n")
			os.Exit(127)
		}
		os.Exit(0) // unreachable
	}
	os.Exit(m.Run())
}

// buildExitChildExe compiles a tiny helper that prints its env then
// exits with the requested code. Used as the "real binary" execReal
// should spawn-and-wait.
func buildExitChildExe(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "exitchild.go")
	contents := `package main

import (
	"fmt"
	"os"
	"strconv"
)

func main() {
	code := 0
	if len(os.Args) > 1 {
		if n, err := strconv.Atoi(os.Args[1]); err == nil {
			code = n
		}
	}
	fmt.Println("RAN")
	fmt.Println("MARKER=" + os.Getenv("__JITENV_INJECTED"))
	fmt.Println("WARNED=" + os.Getenv("__JITENV_AGENT_WARNED"))
	os.Exit(code)
}
`
	if err := os.WriteFile(src, []byte(contents), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(t.TempDir(), "exitchild.exe")
	cmd := exec.Command("go", "build", "-o", out, src)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build exitchild: %v", err)
	}
	return out
}

// TestExecRealExitCodePropagation is the headline test for the Windows
// shim spawn-and-wait path: a child that exits with code 17 must cause
// execReal to exit with 17. We re-exec ourselves through TestMain's
// driver branch so execReal's os.Exit doesn't terminate the test
// runner. Env markers (__JITENV_INJECTED, __JITENV_AGENT_WARNED) are
// passed through cmd.Env -> exec.Cmd.Env -> child process.
func TestExecRealExitCodePropagation(t *testing.T) {
	child := buildExitChildExe(t)

	cmd := exec.Command(os.Args[0], "17")
	cmd.Env = append(os.Environ(),
		"JITENV_TEST_EXEC_REAL_TARGET="+child,
		"__JITENV_INJECTED=1",
	)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()

	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected ExitError; got err=%v output=%s", err, buf.String())
	}
	if ee.ExitCode() != 17 {
		t.Fatalf("expected exit 17; got %d output=%s", ee.ExitCode(), buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "RAN") {
		t.Errorf("child did not print RAN; output=%s", out)
	}
	if !strings.Contains(out, "MARKER=1") {
		t.Errorf("__JITENV_INJECTED did not propagate through spawn-and-wait; output=%s", out)
	}
}

// TestExecRealZeroExit verifies the happy-path exit mapping.
func TestExecRealZeroExit(t *testing.T) {
	child := buildExitChildExe(t)

	cmd := exec.Command(os.Args[0], "0")
	cmd.Env = append(os.Environ(),
		"JITENV_TEST_EXEC_REAL_TARGET="+child,
		"__JITENV_AGENT_WARNED=1",
	)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("expected exit 0; got %v output=%s", err, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "RAN") {
		t.Errorf("child did not print RAN; output=%s", out)
	}
	if !strings.Contains(out, "WARNED=1") {
		t.Errorf("__JITENV_AGENT_WARNED did not propagate; output=%s", out)
	}
}

// TestExecRealMissingBinary verifies the error path: when the target
// doesn't exist, execReal must return an error (not os.Exit) so the
// caller can surface it. We use a unique non-existent path so the
// failure mode is unambiguous.
func TestExecRealMissingBinary(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist-"+strconv.Itoa(os.Getpid())+".exe")

	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(),
		"JITENV_TEST_EXEC_REAL_TARGET="+missing,
	)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected non-zero exit when target is missing; output=%s", buf.String())
	}
	// Exit code 127 (our driver's wrapper) confirms execReal returned
	// an error rather than os.Exit-ing.
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected ExitError; got %v", err)
	}
	if ee.ExitCode() != 127 {
		t.Fatalf("expected exit 127 (error path); got %d output=%s", ee.ExitCode(), buf.String())
	}
}
