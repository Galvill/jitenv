package syncconfig_test

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gv/jitenv/internal/crypto"
	"github.com/gv/jitenv/internal/syncadapters"
	_ "github.com/gv/jitenv/internal/syncadapters/file"
	"github.com/gv/jitenv/internal/syncconfig"
	"github.com/gv/jitenv/pkg/syncadapter"
)

const samplePassphrase = "correct horse battery staple"

// sampleConfig is a minimal but valid-shaped config.toml payload. The
// engine treats it as opaque bytes; byte-identity across the round-trip
// is what matters.
const sampleConfig = `version = 1

[_meta]
kdf = "argon2id"
salt = "AAAAAAAAAAAAAAAAAAAAAA=="
verify = "enc:v2:xxx"

[sources.local]
type = "local"
`

// newMachine builds a fresh sidecar (machine 1) with a wrapped DEK and a
// file adapter pointing at remotePath. Returns the sidecar + master key +
// dek (both zeroable by caller is the test's concern; ignored here).
func newMachine(t *testing.T, remotePath string) *syncconfig.File {
	t.Helper()
	salt, err := crypto.NewSalt()
	if err != nil {
		t.Fatal(err)
	}
	p := crypto.DefaultArgonParams()
	f := &syncconfig.File{
		Version:        syncconfig.Version,
		Salt:           base64.StdEncoding.EncodeToString(salt),
		ArgonTime:      p.Time,
		ArgonMemoryKiB: p.MemKiB,
		ArgonThreads:   p.Threads,
	}
	mk, _ := f.DeriveMasterKey([]byte(samplePassphrase))
	dek, _ := syncconfig.NewDEK()
	if err := f.WrapDEK(mk, dek); err != nil {
		t.Fatal(err)
	}
	f.Adapters = append(f.Adapters, syncconfig.Adapter{
		Name: "remote", Type: "file", Params: map[string]any{"path": remotePath},
	})
	return f
}

func unlock(t *testing.T, f *syncconfig.File, pass string) (masterKey, dek []byte) {
	t.Helper()
	mk, err := f.DeriveMasterKey([]byte(pass))
	if err != nil {
		t.Fatal(err)
	}
	d, err := f.UnwrapDEK(mk)
	if err != nil {
		t.Fatal(err)
	}
	// CLAUDE.md: key material that lives outside the agent must be
	// zeroed on defer. Tests run after the test body via t.Cleanup.
	t.Cleanup(func() {
		zeroBytes(mk)
		zeroBytes(d)
	})
	return mk, d
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

func buildFileAdapter(t *testing.T, mk []byte, ad *syncconfig.Adapter) syncadapter.Adapter {
	t.Helper()
	params, err := syncconfig.DecryptParams(mk, ad)
	if err != nil {
		t.Fatal(err)
	}
	built, err := syncadapters.Build(ad.Type, params)
	if err != nil {
		t.Fatal(err)
	}
	return built
}

// TestTwoMachineRoundTrip is the core paranoid test: machine 1 pushes,
// machine 2 (same passphrase, separate sidecar) pulls and ends up with a
// byte-identical config.
func TestTwoMachineRoundTrip(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "blob")

	// Machine 1 pushes sampleConfig.
	m1 := newMachine(t, remote)
	mk1, dek1 := unlock(t, m1, samplePassphrase)
	a1 := buildFileAdapter(t, mk1, &m1.Adapters[0])
	if _, err := syncconfig.PushConfig(context.Background(), a1, &m1.Adapters[0], dek1, []byte(sampleConfig), 1, false); err != nil {
		t.Fatalf("m1 push: %v", err)
	}

	// Machine 2: SAME passphrase, but its own freshly-generated DEK is
	// WRONG — it must adopt machine 1's wrapped DEK to decrypt. Simulate
	// the real flow: machine 2's sidecar copies the same salt and the
	// SAME wrapped DEK (this is what `jitenv sync init` would reproduce
	// if it shared the wrapped DEK; here we model the "DEK is per-config,
	// distributed out of band via the wrapped form" invariant by reusing
	// m1's wrapped DEK).
	m2 := &syncconfig.File{
		Version:        m1.Version,
		Salt:           m1.Salt,
		ArgonTime:      m1.ArgonTime,
		ArgonMemoryKiB: m1.ArgonMemoryKiB,
		ArgonThreads:   m1.ArgonThreads,
		WrappedDEK:     m1.WrappedDEK,
		Adapters: []syncconfig.Adapter{
			{Name: "remote", Type: "file", Params: map[string]any{"path": remote}},
		},
	}
	mk2, dek2 := unlock(t, m2, samplePassphrase)
	a2 := buildFileAdapter(t, mk2, &m2.Adapters[0])

	// Machine 2 starts from an empty/old local config.
	localOnM2 := []byte("version = 1\n")
	res, err := syncconfig.PullConfig(context.Background(), a2, &m2.Adapters[0], dek2, localOnM2, true /*adopt: first pull on a new machine*/)
	if err != nil {
		t.Fatalf("m2 pull: %v", err)
	}
	if res.Decision != syncconfig.DecideFastForward {
		t.Fatalf("expected fast-forward, got %v", res.Decision)
	}
	if string(res.Applied) != sampleConfig {
		t.Fatalf("pulled config not byte-identical:\n got: %q\nwant: %q", res.Applied, sampleConfig)
	}
}

// TestWrongPassphrasePullFailsClosed: a different passphrase derives a
// different master key, cannot unwrap the DEK -> hard error.
func TestWrongPassphrasePullFailsClosed(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "blob")
	m1 := newMachine(t, remote)
	mk1, dek1 := unlock(t, m1, samplePassphrase)
	a1 := buildFileAdapter(t, mk1, &m1.Adapters[0])
	if _, err := syncconfig.PushConfig(context.Background(), a1, &m1.Adapters[0], dek1, []byte(sampleConfig), 1, false); err != nil {
		t.Fatal(err)
	}

	// Wrong passphrase -> UnwrapDEK fails before we ever touch the remote.
	wrongMK, err := m1.DeriveMasterKey([]byte("totally wrong passphrase"))
	if err != nil {
		t.Fatal(err)
	}
	if _, uerr := m1.UnwrapDEK(wrongMK); uerr == nil {
		t.Fatal("expected unwrap with wrong passphrase to fail")
	}
}

// TestDivergenceFenceLeavesLocalUntouched: both sides advance past base
// -> pull returns DivergenceError and Applied is nil.
func TestDivergenceFenceLeavesLocalUntouched(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "blob")
	m1 := newMachine(t, remote)
	mk1, dek1 := unlock(t, m1, samplePassphrase)
	a1 := buildFileAdapter(t, mk1, &m1.Adapters[0])

	// Establish a common base by pushing v0.
	base := []byte("version = 1\n# base\n")
	if _, err := syncconfig.PushConfig(context.Background(), a1, &m1.Adapters[0], dek1, base, 1, false); err != nil {
		t.Fatal(err)
	}

	// Remote advances (another machine pushed a new version with --force-
	// equivalent semantics: we just push a different payload directly).
	remoteV2 := []byte("version = 1\n# remote edit\n")
	if _, err := syncconfig.PushConfig(context.Background(), a1, &m1.Adapters[0], dek1, remoteV2, 1, true); err != nil {
		t.Fatal(err)
	}
	// But m1's recorded base is still pinned at remoteV2's hash now. Reset
	// it to the original base to model "remote moved, our base is old".
	m1.Adapters[0].BaseHash = syncconfig.HashConfig(base)

	// Local also advanced past base.
	localV2 := []byte("version = 1\n# local edit\n")

	res, err := syncconfig.PullConfig(context.Background(), a1, &m1.Adapters[0], dek1, localV2, false)
	var div *syncconfig.DivergenceError
	if !errors.As(err, &div) {
		t.Fatalf("expected DivergenceError, got %v", err)
	}
	if res.Decision != syncconfig.DecideDiverged {
		t.Fatalf("expected DecideDiverged, got %v", res.Decision)
	}
	if res.Applied != nil {
		t.Fatal("divergence must not produce an Applied payload (local untouched)")
	}
}

// TestNoBaseWithoutAdoptAborts: a fresh machine (no base) where local
// differs from a populated remote must NOT silently clobber local unless
// the user opts in with adopt=true.
func TestNoBaseWithoutAdoptAborts(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "blob")
	m1 := newMachine(t, remote)
	mk1, dek1 := unlock(t, m1, samplePassphrase)
	a1 := buildFileAdapter(t, mk1, &m1.Adapters[0])
	if _, err := syncconfig.PushConfig(context.Background(), a1, &m1.Adapters[0], dek1, []byte(sampleConfig), 1, false); err != nil {
		t.Fatal(err)
	}

	// Fresh machine: no base, local differs from remote, adopt=false.
	m2 := &syncconfig.File{
		Version: m1.Version, Salt: m1.Salt, ArgonTime: m1.ArgonTime,
		ArgonMemoryKiB: m1.ArgonMemoryKiB, ArgonThreads: m1.ArgonThreads,
		WrappedDEK: m1.WrappedDEK,
		Adapters:   []syncconfig.Adapter{{Name: "remote", Type: "file", Params: map[string]any{"path": remote}}},
	}
	mk2, dek2 := unlock(t, m2, samplePassphrase)
	a2 := buildFileAdapter(t, mk2, &m2.Adapters[0])

	res, err := syncconfig.PullConfig(context.Background(), a2, &m2.Adapters[0], dek2, []byte("version = 1\n# local stuff\n"), false)
	var div *syncconfig.DivergenceError
	if !errors.As(err, &div) {
		t.Fatalf("expected NoBase abort, got %v", err)
	}
	if res.Applied != nil {
		t.Fatal("NoBase abort must leave local untouched (Applied nil)")
	}
}

// TestPushFenceRejectsStaleOverwrite: remote advanced past our base, a
// non-force push is refused.
func TestPushFenceRejectsStaleOverwrite(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "blob")
	m1 := newMachine(t, remote)
	mk1, dek1 := unlock(t, m1, samplePassphrase)
	a1 := buildFileAdapter(t, mk1, &m1.Adapters[0])

	base := []byte("version = 1\n")
	if _, err := syncconfig.PushConfig(context.Background(), a1, &m1.Adapters[0], dek1, base, 1, false); err != nil {
		t.Fatal(err)
	}
	// Remote moves on (force push of a newer payload).
	if _, err := syncconfig.PushConfig(context.Background(), a1, &m1.Adapters[0], dek1, []byte("version = 1\n# newer\n"), 1, true); err != nil {
		t.Fatal(err)
	}
	// Pin our base back to the old payload.
	m1.Adapters[0].BaseHash = syncconfig.HashConfig(base)

	// A non-force push of yet another local edit must be refused.
	_, err := syncconfig.PushConfig(context.Background(), a1, &m1.Adapters[0], dek1, []byte("version = 1\n# my edit\n"), 1, false)
	if err == nil {
		t.Fatal("expected stale push to be refused by the fence")
	}
}

// TestPushFenceRejectsIncompleteRemote: an orphan blob on the remote
// (meta sidecar manually removed, or a partial-write that lost the
// meta) must NOT be silently clobbered by a non-force push — the
// engine must surface ErrRemoteStateIncomplete and require --force
// (#279).
func TestPushFenceRejectsIncompleteRemote(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "blob")
	m1 := newMachine(t, remote)
	mk1, dek1 := unlock(t, m1, samplePassphrase)
	a1 := buildFileAdapter(t, mk1, &m1.Adapters[0])

	// Publish a clean (blob, meta) pair.
	if _, err := syncconfig.PushConfig(context.Background(), a1, &m1.Adapters[0], dek1, []byte(sampleConfig), 1, false); err != nil {
		t.Fatal(err)
	}
	// Corrupt the remote by deleting the meta sidecar (mirrors a
	// partial Push or a partial filesystem replication).
	if err := os.Remove(remote + ".meta.json"); err != nil {
		t.Fatal(err)
	}

	// A non-force push must refuse.
	if _, err := syncconfig.PushConfig(context.Background(), a1, &m1.Adapters[0], dek1, []byte("version = 1\n# new\n"), 1, false); err == nil {
		t.Fatal("expected push against incomplete remote to be refused without --force")
	}

	// --force still works.
	if _, err := syncconfig.PushConfig(context.Background(), a1, &m1.Adapters[0], dek1, []byte("version = 1\n# new\n"), 1, true); err != nil {
		t.Fatalf("--force push against incomplete remote should succeed: %v", err)
	}
}

// TestPullRefusesIncompleteRemote: pull against an orphan blob/meta
// must surface a clear error rather than fall through Decide() with a
// zero-hash remote (which would otherwise look like spurious
// divergence). The local config must remain untouched (#279).
func TestPullRefusesIncompleteRemote(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "blob")
	m1 := newMachine(t, remote)
	mk1, dek1 := unlock(t, m1, samplePassphrase)
	a1 := buildFileAdapter(t, mk1, &m1.Adapters[0])

	if _, err := syncconfig.PushConfig(context.Background(), a1, &m1.Adapters[0], dek1, []byte(sampleConfig), 1, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(remote + ".meta.json"); err != nil {
		t.Fatal(err)
	}

	res, err := syncconfig.PullConfig(context.Background(), a1, &m1.Adapters[0], dek1, []byte(sampleConfig), false)
	if err == nil {
		t.Fatal("expected pull against incomplete remote to fail")
	}
	if res.Applied != nil {
		t.Fatal("incomplete-remote pull must not produce an Applied payload")
	}
}

// TestPushFenceRejectsNoBaseOverwrite: a fresh machine with no recorded
// base, pushing to a remote that already has DIFFERENT state, must be
// refused on a non-force push (symmetric with PullConfig's no-base
// fence) so it cannot silently clobber the remote. --force still works.
func TestPushFenceRejectsNoBaseOverwrite(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "blob")

	// Machine 1 publishes the authoritative remote state.
	m1 := newMachine(t, remote)
	mk1, dek1 := unlock(t, m1, samplePassphrase)
	a1 := buildFileAdapter(t, mk1, &m1.Adapters[0])
	if _, err := syncconfig.PushConfig(context.Background(), a1, &m1.Adapters[0], dek1, []byte(sampleConfig), 1, false); err != nil {
		t.Fatal(err)
	}

	// Machine 2: same passphrase, no recorded base, different local edit.
	m2 := &syncconfig.File{
		Version: m1.Version, Salt: m1.Salt, ArgonTime: m1.ArgonTime,
		ArgonMemoryKiB: m1.ArgonMemoryKiB, ArgonThreads: m1.ArgonThreads,
		WrappedDEK: m1.WrappedDEK,
		Adapters:   []syncconfig.Adapter{{Name: "remote", Type: "file", Params: map[string]any{"path": remote}}},
	}
	mk2, dek2 := unlock(t, m2, samplePassphrase)
	a2 := buildFileAdapter(t, mk2, &m2.Adapters[0])
	if m2.Adapters[0].BaseHash != "" {
		t.Fatal("precondition: fresh machine must have empty base")
	}

	localEdit := []byte("version = 1\n# machine 2 edit\n")
	if _, err := syncconfig.PushConfig(context.Background(), a2, &m2.Adapters[0], dek2, localEdit, 1, false); err == nil {
		t.Fatal("expected no-base push over existing remote to be refused")
	}

	// --force overrides the fence.
	if _, err := syncconfig.PushConfig(context.Background(), a2, &m2.Adapters[0], dek2, localEdit, 1, true); err != nil {
		t.Fatalf("forced push should succeed: %v", err)
	}
}

// casRejectAdapter wraps an existing adapter and unconditionally returns
// ErrPreconditionFailed from Push, simulating a CAS-capable backend (s3)
// where a concurrent writer landed between the pre-push Pull and the
// Push and the storage rejected our write. Pull and Validate delegate so
// the engine's pre-push Pull still sees a real remote.
type casRejectAdapter struct{ inner syncadapter.Adapter }

func (c *casRejectAdapter) Name() string                       { return c.inner.Name() }
func (c *casRejectAdapter) Validate(ctx context.Context) error { return c.inner.Validate(ctx) }
func (c *casRejectAdapter) Pull(ctx context.Context) ([]byte, syncadapter.Meta, error) {
	return c.inner.Pull(ctx)
}
func (c *casRejectAdapter) Push(_ context.Context, _ []byte, _ syncadapter.Meta) error {
	return syncadapters.ErrPreconditionFailed
}

// TestPushCASRejectOnForceHasNonCircularMessage: when --force is set and
// the storage-level CAS still rejects the push (the engine's soft fence
// was bypassed by --force, but the adapter's CAS — e.g. s3 If-Match —
// is an independent layer that --force does NOT disable), the surfaced
// error MUST NOT tell the user to "push with --force to overwrite",
// because the user is already doing exactly that (#278 review). It also
// MUST wrap ErrPreconditionFailed so callers can detect the case
// programmatically.
func TestPushCASRejectOnForceHasNonCircularMessage(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "blob")
	m1 := newMachine(t, remote)
	mk1, dek1 := unlock(t, m1, samplePassphrase)
	inner := buildFileAdapter(t, mk1, &m1.Adapters[0])

	// Seed the remote so the pre-push Pull succeeds (otherwise the
	// engine would never reach the Push call we want to exercise).
	if _, err := syncconfig.PushConfig(context.Background(), inner, &m1.Adapters[0], dek1, []byte(sampleConfig), 1, false); err != nil {
		t.Fatal(err)
	}

	// Wrap the adapter so Push always returns ErrPreconditionFailed —
	// the same error a CAS-capable backend produces when a concurrent
	// writer raced us between Pull and Push.
	rejecting := &casRejectAdapter{inner: inner}

	// --force is set; the soft fence will not block, so we reach the
	// adapter's Push, which rejects via CAS.
	_, err := syncconfig.PushConfig(context.Background(), rejecting, &m1.Adapters[0], dek1,
		[]byte("version = 1\n# forced edit\n"), 1, true)
	if err == nil {
		t.Fatal("expected force-push to surface the CAS rejection")
	}
	if !errors.Is(err, syncadapters.ErrPreconditionFailed) {
		t.Fatalf("expected error to wrap ErrPreconditionFailed (for callers using errors.Is), got %v", err)
	}
	msg := err.Error()
	// The remediation must not be circular: telling a user who passed
	// --force to "push with --force" is unhelpful.
	circular := regexp.MustCompile(`--force\s+to\s+overwrite`)
	if circular.MatchString(msg) {
		t.Fatalf("force-push CAS error must not suggest --force again, got: %q", msg)
	}
	// And it must not advise pulling-to-reconcile either: under --force
	// the user has explicitly chosen to overwrite, so the right next
	// step is to retry the push.
	if strings.Contains(msg, "jitenv sync pull") {
		t.Fatalf("force-push CAS error must not advise `jitenv sync pull` (the user already chose to overwrite), got: %q", msg)
	}
	// Sanity: the message should at least mention retrying so the user
	// knows what to do.
	if !strings.Contains(msg, "retry") {
		t.Fatalf("force-push CAS error should suggest a retry, got: %q", msg)
	}
}

// TestPushCASRejectWithoutForceKeepsReconcileAdvice: the non-force case
// keeps the original remediation — pull to reconcile, or escalate to
// --force — because both are valid next steps when the user hasn't yet
// committed to overwriting (#278 review).
func TestPushCASRejectWithoutForceKeepsReconcileAdvice(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "blob")
	m1 := newMachine(t, remote)
	mk1, dek1 := unlock(t, m1, samplePassphrase)
	inner := buildFileAdapter(t, mk1, &m1.Adapters[0])

	// Seed remote so the pre-push Pull yields a known state and our
	// recorded base matches it — the engine's soft fence then passes
	// through and we reach the adapter Push that the wrapper rejects.
	if _, err := syncconfig.PushConfig(context.Background(), inner, &m1.Adapters[0], dek1, []byte(sampleConfig), 1, false); err != nil {
		t.Fatal(err)
	}

	rejecting := &casRejectAdapter{inner: inner}

	// force=false: the soft fence sees rmeta.Hash == ad.BaseHash so it
	// does not block, then the adapter Push rejects with the CAS error.
	_, err := syncconfig.PushConfig(context.Background(), rejecting, &m1.Adapters[0], dek1,
		[]byte("version = 1\n# follow-up edit\n"), 1, false)
	if err == nil {
		t.Fatal("expected non-force push to surface the CAS rejection")
	}
	if !errors.Is(err, syncadapters.ErrPreconditionFailed) {
		t.Fatalf("expected error to wrap ErrPreconditionFailed, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "jitenv sync pull") {
		t.Fatalf("non-force CAS error should advise `jitenv sync pull`, got: %q", msg)
	}
	if !strings.Contains(msg, "--force to overwrite") {
		t.Fatalf("non-force CAS error should mention escalating to --force, got: %q", msg)
	}
}
