//go:build windows

package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/gv/jitenv/internal/config"
)

// buildBinaryWindows compiles the jitenv binary into the test's tempdir
// and returns its path. Mirrors the Unix daemonize_test helper but emits
// a .exe under Windows.
func buildBinaryWindows(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "jitenv.exe")
	cmd := exec.Command("go", "build", "-o", out, "../../cmd/jitenv")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}
	return out
}

// TestSpawnDaemonEndToEndWindows exercises the Stage 2.2 hidden-console
// daemon spawn + key-handle handoff. It cannot call SpawnDaemon directly
// because that re-execs os.Executable(); under `go test` that's the
// compiled test binary, which doesn't speak the __agent subcommand. So
// we build a real jitenv.exe and replicate SpawnDaemon's handshake
// against it (anonymous pipe, --key-handle=<hex>,
// AdditionalInheritedHandles, CREATE_NO_WINDOW | DETACHED_PROCESS,
// hidden window).
//
// The "no visible console" property is hard to assert programmatically;
// reachability + clean shutdown via OpLock are what we cover here. CI
// running this on a headless windows-latest runner is the closest
// practical proxy for "doesn't pop a console window."
func TestSpawnDaemonEndToEndWindows(t *testing.T) {
	if os.Getenv("CI_NO_BUILD") != "" {
		t.Skip("skipping daemon e2e in CI_NO_BUILD")
	}
	bin := buildBinaryWindows(t)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2-daemon-win")
	if err := config.InitNew(cfgPath, pw); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	key, err := config.DeriveKeyFromMeta(cfg, pw)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	defer func() {
		for i := range key {
			key[i] = 0
		}
	}()

	// Per-test runtime dir: LOCALAPPDATA points the per-user runtime
	// base directory at our tempdir so the pidfile + logs don't pollute
	// the real user profile. The pipe name is per-user (SID-derived) and
	// is the same for parent + child because both inherit the test
	// process's user token.
	runtimeDir := t.TempDir()
	t.Setenv("LOCALAPPDATA", runtimeDir)
	paths, err := DefaultPaths()
	if err != nil {
		t.Fatalf("paths: %v", err)
	}

	// Replicate SpawnDaemon's wiring against the freshly built binary.
	pr, pw2, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	// Clear inherit on the write end so the child only inherits the read end.
	if err := windows.SetHandleInformation(windows.Handle(pw2.Fd()), windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		t.Fatalf("clear inherit on write end: %v", err)
	}

	logF, err := os.OpenFile(paths.LogFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer logF.Close()
	devNull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("open devnull: %v", err)
	}
	defer devNull.Close()

	rHandle := syscall.Handle(pr.Fd())
	cmd := exec.Command(bin,
		"__agent",
		"--key-handle="+strconv.FormatUint(uint64(rHandle), 16),
		"--config="+cfgPath,
		"--idle=10s",
	)
	cmd.Env = append(os.Environ(), "LOCALAPPDATA="+runtimeDir)
	cmd.Stdin = devNull
	cmd.Stdout = logF
	cmd.Stderr = logF
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:                 true,
		CreationFlags:              windows.DETACHED_PROCESS | windows.CREATE_NO_WINDOW,
		AdditionalInheritedHandles: []syscall.Handle{rHandle},
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pr.Close()
	if _, err := pw2.Write(key); err != nil {
		t.Fatalf("write key: %v", err)
	}
	pw2.Close()
	defer func() { _ = cmd.Process.Kill() }()

	// Surface the agent's log on failure — useful when the child fails
	// to bind its pipe (config error, missing inherited handle, etc.)
	// and the test only sees "connect agent: file not found".
	dumpLog := func() {
		if !t.Failed() {
			return
		}
		// The child may still hold a write handle to the log file when
		// the test fails (we haven't Wait'd yet). On Windows that
		// usually still allows reads thanks to default FILE_SHARE_READ;
		// best-effort either way.
		b, err := os.ReadFile(paths.LogFile)
		if err != nil {
			t.Logf("agent log unreadable: %v", err)
		} else {
			t.Logf("agent log (%s):\n%s", paths.LogFile, string(b))
		}
		// Also wait for the child to expose a process state so we can
		// surface its exit code.
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			if cmd.ProcessState != nil {
				t.Logf("agent exit: %s", cmd.ProcessState)
			}
		}
	}
	t.Cleanup(dumpLog)

	// Wait for the pipe to be reachable, then issue a Status request.
	cli := NewClient(paths.Socket)
	var st *Status
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		st, lastErr = cli.Status(context.Background())
		if lastErr == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("status: %v", lastErr)
	}
	if st == nil || st.PID == 0 {
		t.Fatalf("expected pid in status, got %+v", st)
	}

	// Lock cleanly shuts the agent down via OpLock — pipe-close-as-shutdown.
	if err := cli.Lock(context.Background()); err != nil {
		t.Fatalf("lock: %v", err)
	}
	_, _ = cmd.Process.Wait()

	// After Lock + Wait, the listener should be gone.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	conn, derr := dialAgent(ctx, paths.Socket, 500*time.Millisecond)
	if derr == nil {
		conn.Close()
		t.Fatalf("expected pipe %s to be gone after Lock", paths.Socket)
	}
}

// TestPidAliveWindows is a regression guard against Stage 1's Unix-only
// PidAlive returning false for every pid on Windows. SpawnDaemon's
// "agent already running?" check depends on this returning true for the
// current process.
func TestPidAliveWindows(t *testing.T) {
	if !PidAlive(os.Getpid()) {
		t.Fatalf("PidAlive returned false for self (pid %d)", os.Getpid())
	}
	// Pid 0 is the System Idle Process — OpenProcess rejects it as an
	// invalid parameter, so PidAlive must report false.
	if PidAlive(0) {
		t.Fatalf("PidAlive returned true for pid 0")
	}
	// A reasonably impossible pid on a healthy Windows box.
	if PidAlive(0x7fffffff) {
		t.Fatalf("PidAlive returned true for absurd pid")
	}
}
