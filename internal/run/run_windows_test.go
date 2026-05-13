//go:build windows

package run_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildJitenv compiles the jitenv binary into the test's temp dir.
// Mirrors the Unix helper but writes to jitenv.exe; the build command
// inherits GOOS=windows because tests in this file only compile under
// //go:build windows.
func buildJitenv(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "jitenv.exe")
	cmd := exec.Command("go", "build", "-o", out, "../../cmd/jitenv")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build jitenv: %v", err)
	}
	return out
}

// buildExitChild compiles a tiny helper that exits with the requested
// code and prints any env markers we care about to stdout. We don't
// use //go:embed or testdata source: a tiny one-file program in a
// temp dir compiled via `go build` is the same pattern run_e2e_test.go
// uses on Unix to produce shell-script targets, just adapted for a
// Go-binary target (Windows doesn't have a /bin/sh interpreter we can
// rely on in CI).
func buildExitChild(t *testing.T) string {
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
	// Echo the markers so the test can confirm env propagation.
	fmt.Println("MARKER=" + os.Getenv("__JITENV_INJECTED"))
	fmt.Println("WARNED=" + os.Getenv("__JITENV_AGENT_WARNED"))
	fmt.Println("FOO=" + os.Getenv("FOO"))
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

// TestRunWindowsExitCodePropagation is the headline test for the
// Windows spawn-and-wait path: a child that exits with code 42 must
// cause `jitenv run` itself to exit with code 42. The Unix path gets
// this for free because syscall.Exec replaces the process image; on
// Windows we have to forward the code explicitly via os.Exit.
//
// We piggyback on the __JITENV_INJECTED=1 short-circuit so the test
// doesn't need a running agent (the agent runtime port is #87 and is
// not done at the time this test was written). The short-circuit
// branch in run.go reaches replaceProcess directly, exercising the
// same code path as the steady-state agent-up scenario from
// replaceProcess's point of view.
func TestRunWindowsExitCodePropagation(t *testing.T) {
	bin := buildJitenv(t)
	child := buildExitChild(t)

	env := append(os.Environ(),
		"__JITENV_INJECTED=1",
		"FOO=hello",
	)

	cmd := exec.Command(bin, "run", child, "42")
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	// Expect non-nil err, but with exit code 42.
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("jitenv run: expected ExitError with code 42; got err=%v stderr=%s", err, stderr.String())
	}
	if ee.ExitCode() != 42 {
		t.Fatalf("jitenv run: expected exit code 42; got %d stderr=%s", ee.ExitCode(), stderr.String())
	}

	out := stdout.String()
	if !strings.Contains(out, "MARKER=1") {
		t.Errorf("__JITENV_INJECTED did not propagate through spawn-and-wait; stdout=%q", out)
	}
	if !strings.Contains(out, "FOO=hello") {
		t.Errorf("FOO did not propagate to child; stdout=%q", out)
	}
}

// TestRunWindowsZeroExit verifies the happy-path exit-code mapping:
// child exits 0, jitenv run exits 0.
func TestRunWindowsZeroExit(t *testing.T) {
	bin := buildJitenv(t)
	child := buildExitChild(t)

	env := append(os.Environ(),
		"__JITENV_INJECTED=1",
	)
	cmd := exec.Command(bin, "run", child, "0")
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("jitenv run: expected exit 0; got %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "MARKER=1") {
		t.Errorf("marker missing; stdout=%q", stdout.String())
	}
}
