package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gv/jitenv/internal/config"
)

// fakeCwdResolver is a stand-in resolver whose HasCwdMappings flag we
// can flip so the agent's sentinel-file logic is observable in tests.
type fakeCwdResolver struct{ hasCwd bool }

func (f *fakeCwdResolver) Sources() []string               { return nil }
func (f *fakeCwdResolver) IsMapped(string) bool            { return false }
func (f *fakeCwdResolver) HasCwdMappings() bool            { return f.hasCwd }
func (f *fakeCwdResolver) IsMappedCwd(string, string) bool { return false }
func (f *fakeCwdResolver) FetchEnv(context.Context, string) (map[string]string, error) {
	return nil, nil
}
func (f *fakeCwdResolver) FetchEnvCwd(context.Context, string, string) (map[string]string, error) {
	return nil, nil
}

// TestHasCwdSentinelLifecycle covers the user-reported flow: a cwd
// mapping is configured, Listen creates the has-cwd sentinel; Lock
// (or Shutdown) must remove it so the bash hook's bare-PATH branch
// short-circuits silently rather than calling the agent.
func TestHasCwdSentinelLifecycle(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", tmp)
	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	flag := filepath.Join(paths.Dir, "has-cwd")

	res := &fakeCwdResolver{hasCwd: true}
	a := NewAgent(paths, 0, res)
	if err := a.Listen(); err != nil {
		t.Fatalf("listen: %v", err)
	}
	if _, err := os.Stat(flag); err != nil {
		t.Errorf("after Listen: expected has-cwd to exist; got %v", err)
	}

	go a.Serve(context.Background()) //nolint:errcheck
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(paths.Socket); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	a.Shutdown()
	if _, err := os.Stat(flag); !os.IsNotExist(err) {
		t.Errorf("after Shutdown: expected has-cwd removed; stat returned %v", err)
	}
}

// TestRefreshCwdSentinel_ConfigChange covers the OpReload path: a new
// config without cwd mappings should remove the sentinel even if the
// previous config had one.
func TestRefreshCwdSentinel_ConfigChange(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", tmp)
	paths, _ := DefaultPaths()
	flag := filepath.Join(paths.Dir, "has-cwd")

	a := NewAgent(paths, 0, &fakeCwdResolver{hasCwd: true})
	if err := a.Listen(); err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer a.Shutdown()

	if _, err := os.Stat(flag); err != nil {
		t.Fatalf("sentinel missing after Listen: %v", err)
	}

	// Simulate reload returning a resolver with no cwd mappings.
	a.SetReload(func() (Resolver, error) {
		return &fakeCwdResolver{hasCwd: false}, nil
	})

	// Drive the OpReload path the same way dispatch does.
	a.mu.Lock()
	fn := a.reload
	a.mu.Unlock()
	next, err := fn()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	a.mu.Lock()
	a.resolver = next
	a.mu.Unlock()
	a.refreshCwdSentinel()

	if _, err := os.Stat(flag); !os.IsNotExist(err) {
		t.Errorf("after reload removing cwd mappings, expected sentinel gone; got %v", err)
	}
}

// Compile-time guard: the fake satisfies the full Resolver interface.
var _ Resolver = (*fakeCwdResolver)(nil)

// silence the import.
var _ = config.Version
