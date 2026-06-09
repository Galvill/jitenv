package cli

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/crypto"
	_ "github.com/gv/jitenv/internal/syncadapters/file"
	"github.com/gv/jitenv/internal/syncconfig"
)

// TestWritePulledConfig_AcceptsEncryptedFormWithSourceBackedVar is the
// #274 regression: pulling a config whose on-disk var.source is an
// enc:v2 envelope (and whose Sources map is keyed by opaque s_xxxxxx IDs,
// post-#248) must NOT spuriously fail validation. The bug was that
// writePulledConfig called Validate(), which runs ValidatePost() and
// cross-references var.source against Sources keys — those never match
// in encrypted form, so every realistic pull was rejected.
//
// The fix is to call ValidateStructure(), the encrypted-form-safe
// variant. This test reproduces the production shape end-to-end:
//  1. Build an encrypted config with a source-backed var (Sources
//     keyed by opaque IDs, var.source sealed as enc:v2:...).
//  2. Read those raw on-disk bytes (the exact bytes sync push sends).
//  3. Hand them to writePulledConfig — must succeed and write the file.
func TestWritePulledConfig_AcceptsEncryptedFormWithSourceBackedVar(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	pw := []byte(testPassphrase)

	// Init a fresh encrypted config, then add a source + source-backed
	// var and re-seal so the on-disk form has the post-#248 shape
	// (Sources keyed by s_xxxxxx, vars[].source as enc:v2 envelope).
	if err := config.InitNew(cfgPath, pw); err != nil {
		t.Fatalf("InitNew: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	key, err := config.DeriveKeyFromMeta(cfg, pw)
	if err != nil {
		t.Fatalf("DeriveKeyFromMeta: %v", err)
	}
	defer func() {
		for i := range key {
			key[i] = 0
		}
	}()
	if err := config.DecryptInPlace(cfg, key); err != nil {
		t.Fatalf("DecryptInPlace: %v", err)
	}
	cfg.Sources = map[string]config.SourceConfig{"vault": {Type: "local"}}
	cfg.Secrets = map[string]map[string]string{"stripe": {"STRIPE_SK": "sk-Y"}}
	cfg.Mappings = []config.Mapping{
		{
			Path: "/abs/run.sh",
			Vars: []config.VarRef{
				{Name: "DATABASE_URL", Source: "vault", Ref: "stripe", Key: "STRIPE_SK"},
			},
		},
	}
	if err := config.EncryptInPlace(cfg, key); err != nil {
		t.Fatalf("EncryptInPlace: %v", err)
	}
	if err := config.AtomicSave(cfgPath, cfg); err != nil {
		t.Fatalf("AtomicSave: %v", err)
	}

	// Sanity: confirm the on-disk var.source IS an envelope and Sources
	// is keyed by an opaque ID — those are the conditions that defeat
	// the old Validate() call.
	onDisk, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("re-Load: %v", err)
	}
	if len(onDisk.Sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(onDisk.Sources))
	}
	for k := range onDisk.Sources {
		if len(k) < 2 || k[0] != 's' || k[1] != '_' {
			t.Fatalf("expected opaque s_xxxxxx source key, got %q", k)
		}
	}
	if got := onDisk.Mappings[0].Vars[0].Source; !crypto.IsEnvelope(got) {
		t.Fatalf("expected var.source to be an enc:v2 envelope, got %q", got)
	}

	// Read the raw bytes — these are what the sync engine sends across
	// the wire on a push, and what PullConfig hands back on a pull.
	pulled, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read cfg: %v", err)
	}

	// Apply to a fresh "machine 2" location.
	destDir := t.TempDir()
	destPath := filepath.Join(destDir, "config.toml")

	// Before the fix this returned `pulled config is invalid, refusing
	// to apply: ... source "enc:v2:..." is not defined`.
	if err := writePulledConfig(destPath, pulled); err != nil {
		t.Fatalf("writePulledConfig must accept a real encrypted config: %v", err)
	}

	// File written, 0600, byte-identical to source.
	info, err := os.Stat(destPath)
	if err != nil {
		t.Fatalf("stat dest: %v", err)
	}
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0600 {
			t.Errorf("dest perms = %o, want 0600", perm)
		}
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != string(pulled) {
		t.Error("dest bytes differ from pulled bytes")
	}
}

// TestWritePulledConfig_RejectsStructurallyInvalid keeps the negative
// path honest: ValidateStructure() still catches shape errors that
// don't require decryption (e.g. a mapping with no path / glob / cwd).
func TestWritePulledConfig_RejectsStructurallyInvalid(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "bad.toml")

	// Hand-rolled invalid config: version is set but the mapping sets
	// none of path/glob/cwd_glob, which ValidateStructure rejects.
	bad := []byte(`version = 1

[_meta]
kdf = "argon2id"
salt = "AAAAAAAAAAAAAAAAAAAAAA=="
verify = "enc:v1:xxx"

[[mappings]]
[[mappings.vars]]
name = "X"
source = "vault"
`)
	err := writePulledConfig(cfgPath, bad)
	if err == nil {
		t.Fatal("writePulledConfig must reject a structurally-invalid pulled config")
	}
}

// TestSyncPullRoundTrip_EncryptedConfigWithSourceBackedVar is the full
// engine-level repro for #274: push a real encrypted config from machine
// 1, pull it on machine 2, and assert writePulledConfig applies the
// result without spuriously rejecting the encrypted form.
func TestSyncPullRoundTrip_EncryptedConfigWithSourceBackedVar(t *testing.T) {
	// Machine 1: build an encrypted config with a source-backed var.
	m1Dir := t.TempDir()
	m1CfgPath := filepath.Join(m1Dir, "config.toml")
	pw := []byte(testPassphrase)
	if err := config.InitNew(m1CfgPath, pw); err != nil {
		t.Fatalf("InitNew: %v", err)
	}
	cfg, err := config.Load(m1CfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	key, err := config.DeriveKeyFromMeta(cfg, pw)
	if err != nil {
		t.Fatalf("DeriveKeyFromMeta: %v", err)
	}
	defer func() {
		for i := range key {
			key[i] = 0
		}
	}()
	if err := config.DecryptInPlace(cfg, key); err != nil {
		t.Fatalf("DecryptInPlace: %v", err)
	}
	cfg.Sources = map[string]config.SourceConfig{"vault": {Type: "local"}}
	cfg.Secrets = map[string]map[string]string{"stripe": {"STRIPE_SK": "sk-Y"}}
	cfg.Mappings = []config.Mapping{
		{
			Path: "/abs/run.sh",
			Vars: []config.VarRef{
				{Name: "DATABASE_URL", Source: "vault", Ref: "stripe", Key: "STRIPE_SK"},
			},
		},
	}
	if err := config.EncryptInPlace(cfg, key); err != nil {
		t.Fatalf("EncryptInPlace: %v", err)
	}
	if err := config.AtomicSave(m1CfgPath, cfg); err != nil {
		t.Fatalf("AtomicSave: %v", err)
	}
	m1Bytes, err := os.ReadFile(m1CfgPath)
	if err != nil {
		t.Fatalf("read m1 cfg: %v", err)
	}

	// Set up the sync sidecar + file adapter, push, then simulate
	// machine 2 pulling.
	remoteDir := t.TempDir()
	remotePath := filepath.Join(remoteDir, "blob")

	salt, err := crypto.NewSalt()
	if err != nil {
		t.Fatal(err)
	}
	p := crypto.DefaultArgonParams()
	syncFile := &syncconfig.File{
		Version:        syncconfig.Version,
		Salt:           base64.StdEncoding.EncodeToString(salt),
		ArgonTime:      p.Time,
		ArgonMemoryKiB: p.MemKiB,
		ArgonThreads:   p.Threads,
	}
	syncMK, err := syncFile.DeriveMasterKey(pw)
	if err != nil {
		t.Fatalf("sync DeriveMasterKey: %v", err)
	}
	defer func() {
		for i := range syncMK {
			syncMK[i] = 0
		}
	}()
	dek, err := syncconfig.NewDEK()
	if err != nil {
		t.Fatalf("NewDEK: %v", err)
	}
	defer func() {
		for i := range dek {
			dek[i] = 0
		}
	}()
	if err := syncFile.WrapDEK(syncMK, dek); err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}
	syncFile.Adapters = []syncconfig.Adapter{
		{Name: "remote", Type: "file", Params: map[string]any{"path": remotePath}},
	}

	pushAdapter, err := buildAdapter(syncMK, &syncFile.Adapters[0])
	if err != nil {
		t.Fatalf("buildAdapter (push): %v", err)
	}
	if _, err := syncconfig.PushConfig(context.Background(), pushAdapter, &syncFile.Adapters[0], dek, m1Bytes, config.Version, false); err != nil {
		t.Fatalf("PushConfig: %v", err)
	}

	// Machine 2: fresh local, no base — adopt the remote on first pull.
	m2Dir := t.TempDir()
	m2CfgPath := filepath.Join(m2Dir, "config.toml")
	m2Local := []byte("version = 1\n")

	pullAdapter, err := buildAdapter(syncMK, &syncFile.Adapters[0])
	if err != nil {
		t.Fatalf("buildAdapter (pull): %v", err)
	}
	// Use a separate adapter snapshot to model machine 2 having its own
	// sidecar with an empty base.
	m2Ad := syncconfig.Adapter{Name: "remote", Type: "file", Params: syncFile.Adapters[0].Params}
	res, err := syncconfig.PullConfig(context.Background(), pullAdapter, &m2Ad, dek, m2Local, true)
	if err != nil {
		t.Fatalf("PullConfig: %v", err)
	}
	if res.Decision != syncconfig.DecideFastForward {
		t.Fatalf("expected DecideFastForward, got %v", res.Decision)
	}
	if string(res.Applied) != string(m1Bytes) {
		t.Fatal("pulled bytes differ from pushed bytes")
	}

	// THE regression: writing the pulled config used to reject it.
	if err := writePulledConfig(m2CfgPath, res.Applied); err != nil {
		t.Fatalf("writePulledConfig must apply a real pulled config: %v", err)
	}

	// Belt-and-braces: the written file decrypts back to the same
	// in-memory shape (a source-backed DATABASE_URL referring to
	// vault/stripe/STRIPE_SK).
	got, err := config.Load(m2CfgPath)
	if err != nil {
		t.Fatalf("re-Load on m2: %v", err)
	}
	m2Key, err := config.DeriveKeyFromMeta(got, pw)
	if err != nil {
		t.Fatalf("m2 DeriveKeyFromMeta: %v", err)
	}
	defer func() {
		for i := range m2Key {
			m2Key[i] = 0
		}
	}()
	if err := config.DecryptInPlace(got, m2Key); err != nil {
		t.Fatalf("m2 DecryptInPlace: %v", err)
	}
	if _, ok := got.Sources["vault"]; !ok {
		t.Fatalf("decrypted m2 missing source 'vault': %v", got.Sources)
	}
	if len(got.Mappings) != 1 || len(got.Mappings[0].Vars) != 1 {
		t.Fatalf("m2 mappings shape wrong: %+v", got.Mappings)
	}
	v := got.Mappings[0].Vars[0]
	if v.Name != "DATABASE_URL" || v.Source != "vault" || v.Ref != "stripe" || v.Key != "STRIPE_SK" {
		t.Fatalf("m2 var content wrong: %+v", v)
	}
}
