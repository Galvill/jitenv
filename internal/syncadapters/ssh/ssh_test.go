package ssh

import (
	"context"
	"strings"
	"testing"

	"github.com/gv/jitenv/internal/syncadapters"
	"github.com/gv/jitenv/pkg/syncadapter"
)

// fakeRemote emulates a remote POSIX host: a single in-memory file store
// keyed by path. It interprets the small set of remote shell commands the
// adapter emits (cat >, cat, test -f, mkdir/test -w).
type fakeRemote struct {
	files map[string][]byte
}

func newFakeRemote() *fakeRemote { return &fakeRemote{files: map[string][]byte{}} }

type fakeExitErr struct{ code int }

func (e fakeExitErr) Error() string { return "exit" }
func (e fakeExitErr) ExitCode() int { return e.code }

func (f *fakeRemote) run(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	// The last arg is the remote command string; earlier args are ssh
	// options + host.
	cmd := args[len(args)-1]
	switch {
	case strings.HasPrefix(cmd, "mkdir -p"):
		return nil, nil // validate probe always succeeds
	case strings.Contains(cmd, "cat >"):
		// write: "umask 077 && cat > 'tmp' && chmod 600 'tmp' && mv 'tmp' 'dst'"
		dst := lastQuoted(cmd)
		f.files[dst] = append([]byte(nil), stdin...)
		return nil, nil
	case strings.HasPrefix(cmd, "if test -f"):
		// read: "if test -f 'p'; then cat 'p'; else exit 66; fi"
		p := firstQuoted(cmd)
		data, ok := f.files[p]
		if !ok {
			return nil, fakeExitErr{code: 66}
		}
		return data, nil
	default:
		return nil, nil
	}
}

// firstQuoted returns the first single-quoted token in s.
func firstQuoted(s string) string {
	i := strings.IndexByte(s, '\'')
	if i < 0 {
		return ""
	}
	j := strings.IndexByte(s[i+1:], '\'')
	if j < 0 {
		return ""
	}
	return s[i+1 : i+1+j]
}

// lastQuoted returns the last single-quoted token in s (the mv dest).
func lastQuoted(s string) string {
	i := strings.LastIndexByte(s, '\'')
	if i < 0 {
		return ""
	}
	j := strings.LastIndexByte(s[:i], '\'')
	if j < 0 {
		return ""
	}
	return s[j+1 : i]
}

func newFakeAdapter(t *testing.T, fr *fakeRemote) *adapter {
	t.Helper()
	a, err := New(map[string]any{"host": "user@host", "path": "/srv/jitenv/blob"})
	if err != nil {
		t.Fatal(err)
	}
	ad := a.(*adapter)
	ad.r = fr
	return ad
}

func TestSSHPushPullRoundTrip(t *testing.T) {
	fr := newFakeRemote()
	a := newFakeAdapter(t, fr)

	if _, _, err := a.Pull(context.Background()); err != syncadapters.ErrNoRemoteState {
		t.Fatalf("expected ErrNoRemoteState, got %v", err)
	}

	want := []byte("ciphertext")
	meta := syncadapter.Meta{Hash: "deadbeef", SchemaVersion: 1}
	if err := a.Push(context.Background(), want, meta); err != nil {
		t.Fatalf("push: %v", err)
	}
	got, gotMeta, err := a.Pull(context.Background())
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("blob mismatch: %q", got)
	}
	if gotMeta != meta {
		t.Fatalf("meta mismatch: %+v", gotMeta)
	}
}

func TestSSHRejectsUnsafePath(t *testing.T) {
	if _, err := New(map[string]any{"host": "h", "path": "/srv/$(rm -rf)"}); err == nil {
		t.Fatal("expected unsafe path to be rejected")
	}
	if _, err := New(map[string]any{"host": "h; rm -rf /", "path": "/ok"}); err == nil {
		t.Fatal("expected unsafe host to be rejected")
	}
	if _, err := New(map[string]any{"path": "/ok"}); err == nil {
		t.Fatal("expected missing host to be rejected")
	}
}
