//go:build !windows

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newTestAgentDir returns a short temp dir suitable for an AF_UNIX
// socket. macOS caps sun_path at 104 bytes, and t.TempDir() on macOS
// sits under /var/folders/.../T/<TestName>/NNN/ — already ~90 chars
// before we even append "agent.sock", so long test names blow the
// limit. Use /tmp/jr-* (kernel temp, short prefix) instead.
func newTestAgentDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "jr-")
	if err != nil {
		t.Fatalf("mkdir tmp runtime: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func newTestAgent(t *testing.T, r Resolver) (*Agent, Paths) {
	t.Helper()
	dir := newTestAgentDir(t)
	p := Paths{
		Dir:     dir,
		Socket:  filepath.Join(dir, "agent.sock"),
		PidFile: filepath.Join(dir, "agent.pid"),
		LogFile: filepath.Join(dir, "agent.log"),
	}
	a := NewAgent(p, 0, r)
	if err := a.Listen(); err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(a.Shutdown)
	return a, p
}

type fakeResolver struct {
	mapped map[string]bool
	env    map[string]map[string]string
}

func (f *fakeResolver) Sources() []string      { return []string{"fake"} }
func (f *fakeResolver) IsMapped(p string) bool { return f.mapped[p] }
func (f *fakeResolver) FetchEnv(_ context.Context, p string) (map[string]string, error) {
	return f.env[p], nil
}
func (f *fakeResolver) FetchEnvCwd(_ context.Context, _, _ string) (map[string]string, error) {
	return nil, nil
}
func (f *fakeResolver) CwdCommands(string) []string { return nil }

func TestAgentStatusAndLock(t *testing.T) {
	a, p := newTestAgent(t, nil)

	go a.Serve(context.Background()) //nolint:errcheck // goroutine: error surfaced via Shutdown/Listen pair
	cli := NewClient(p.Socket)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := cli.Status(context.Background()); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	st, err := cli.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.PID == 0 {
		t.Fatalf("status missing pid: %+v", st)
	}

	if err := cli.Lock(context.Background()); err != nil {
		t.Fatalf("lock: %v", err)
	}
	// Wait for shutdown.
	time.Sleep(200 * time.Millisecond)
	if _, err := cli.Status(context.Background()); err == nil {
		t.Fatalf("expected status to fail after lock")
	}
}

func TestAgentResolverDispatch(t *testing.T) {
	fr := &fakeResolver{
		mapped: map[string]bool{"/x": true},
		env:    map[string]map[string]string{"/x": {"FOO": "bar"}},
	}
	a, p := newTestAgent(t, fr)
	go a.Serve(context.Background()) //nolint:errcheck // goroutine: error surfaced via Shutdown/Listen pair
	cli := NewClient(p.Socket)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := cli.Status(context.Background()); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	mapped, err := cli.IsMapped(context.Background(), "/x")
	if err != nil || !mapped {
		t.Fatalf("is_mapped /x: %v %v", mapped, err)
	}
	mapped, err = cli.IsMapped(context.Background(), "/y")
	if err != nil || mapped {
		t.Fatalf("is_mapped /y: %v %v", mapped, err)
	}

	env, err := cli.FetchEnv(context.Background(), "/x")
	if err != nil {
		t.Fatalf("fetch_env: %v", err)
	}
	if env["FOO"] != "bar" {
		t.Fatalf("env: %+v", env)
	}
}

func TestAgentReload(t *testing.T) {
	first := &fakeResolver{
		mapped: map[string]bool{"/a": true},
		env:    map[string]map[string]string{"/a": {"K": "v1"}},
	}
	second := &fakeResolver{
		mapped: map[string]bool{"/a": true, "/b": true},
		env:    map[string]map[string]string{"/a": {"K": "v2"}, "/b": {"X": "y"}},
	}
	a, p := newTestAgent(t, first)
	a.SetReload(func() (Resolver, error) { return second, nil })

	go a.Serve(context.Background()) //nolint:errcheck // goroutine: error surfaced via Shutdown/Listen pair
	cli := NewClient(p.Socket)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := cli.Status(context.Background()); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	env, _ := cli.FetchEnv(context.Background(), "/a")
	if env["K"] != "v1" {
		t.Fatalf("pre-reload K=%q", env["K"])
	}
	if mapped, _ := cli.IsMapped(context.Background(), "/b"); mapped {
		t.Fatalf("pre-reload /b should not be mapped")
	}

	if err := cli.Reload(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}

	env, _ = cli.FetchEnv(context.Background(), "/a")
	if env["K"] != "v2" {
		t.Fatalf("post-reload K=%q (want v2)", env["K"])
	}
	if mapped, _ := cli.IsMapped(context.Background(), "/b"); !mapped {
		t.Fatalf("post-reload /b should be mapped")
	}
}

func TestAgentReloadUnsupported(t *testing.T) {
	a, p := newTestAgent(t, nil)
	go a.Serve(context.Background()) //nolint:errcheck // goroutine: error surfaced via Shutdown/Listen pair
	cli := NewClient(p.Socket)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := cli.Status(context.Background()); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := cli.Reload(context.Background()); err == nil {
		t.Fatalf("expected reload to fail without SetReload")
	}
}

func TestAgentRefusesDoubleListen(t *testing.T) {
	a, p := newTestAgent(t, nil)
	_ = a // keep a alive for duration of test
	a2 := NewAgent(p, 0, nil)
	if err := a2.Listen(); err == nil {
		t.Fatalf("expected second Listen on same paths to fail")
	}
}
