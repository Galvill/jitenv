package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/crypto"
)

const testPassphrase = "correct horse battery staple"

// newImportTestConfig writes a fresh encrypted config to a temp dir,
// optionally seeding a bag, and points JITENV_CONFIG at it. It returns
// the config path and the derived master key (for assertions).
func newImportTestConfig(t *testing.T, seed map[string]map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := config.InitNew(cfgPath, []byte(testPassphrase)); err != nil {
		t.Fatalf("InitNew: %v", err)
	}
	if len(seed) > 0 {
		cfg, err := config.Load(cfgPath)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		key, err := config.DeriveKeyFromMeta(cfg, []byte(testPassphrase))
		if err != nil {
			t.Fatalf("derive: %v", err)
		}
		if err := config.DecryptInPlace(cfg, key); err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		cfg.Secrets = seed
		cfg.Sources = map[string]config.SourceConfig{"local": {Type: "local"}}
		if err := config.EncryptInPlace(cfg, key); err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		if err := config.AtomicSave(cfgPath, cfg); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	t.Setenv("JITENV_CONFIG", cfgPath)
	return cfgPath
}

// readBag decrypts the config at path and returns the named bag's
// real-name keyed cleartext values.
func readBag(t *testing.T, path, bag string) map[string]string {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	key, err := config.DeriveKeyFromMeta(cfg, []byte(testPassphrase))
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if err := config.DecryptInPlace(cfg, key); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	return cfg.Secrets[bag]
}

// runImport drives the bag import command with the given args and
// stdin, injecting a fixed passphrase. Returns stdout, stderr, err.
func runImport(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	prev := importPassphraseFn
	importPassphraseFn = func() ([]byte, error) { return []byte(testPassphrase), nil }
	t.Cleanup(func() { importPassphraseFn = prev })

	cmd := newBagImportCmd()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errBuf.String(), err
}

func TestBagImport_FromFile_NewBag(t *testing.T) {
	cfgPath := newImportTestConfig(t, nil)
	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("FOO=bar\nexport BAZ=\"q u x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := runImport(t, "", "prod", "--from-file", envFile)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !strings.Contains(out, `2 keys (2 new, 0 overwritten, 0 skipped) into bag "prod"`) {
		t.Errorf("summary = %q", out)
	}
	bag := readBag(t, cfgPath, "prod")
	if bag["FOO"] != "bar" || bag["BAZ"] != "q u x" {
		t.Errorf("bag round-trip = %v", bag)
	}
}

func TestBagImport_Stdin(t *testing.T) {
	cfgPath := newImportTestConfig(t, nil)
	out, _, err := runImport(t, "A=1\nB=2\n", "prod", "--stdin")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !strings.Contains(out, "2 new") {
		t.Errorf("summary = %q", out)
	}
	bag := readBag(t, cfgPath, "prod")
	if bag["A"] != "1" || bag["B"] != "2" {
		t.Errorf("bag = %v", bag)
	}
}

func TestBagImport_Stdin_EmptyEOF(t *testing.T) {
	newImportTestConfig(t, nil)
	_, _, err := runImport(t, "", "prod", "--stdin")
	if err == nil || !strings.Contains(err.Error(), "nothing to import") {
		t.Fatalf("want nothing-to-import error, got %v", err)
	}
}

func TestBagImport_FromEnv_MixedPresentMissing(t *testing.T) {
	cfgPath := newImportTestConfig(t, nil)
	t.Setenv("JITENV_TEST_PRESENT", "yes")
	os.Unsetenv("JITENV_TEST_ABSENT")
	out, errOut, err := runImport(t, "", "prod", "--from-env", "JITENV_TEST_PRESENT,JITENV_TEST_ABSENT")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !strings.Contains(out, "1 new") {
		t.Errorf("summary = %q", out)
	}
	if !strings.Contains(errOut, "JITENV_TEST_ABSENT") {
		t.Errorf("expected missing-var warning, got stderr %q", errOut)
	}
	bag := readBag(t, cfgPath, "prod")
	if bag["JITENV_TEST_PRESENT"] != "yes" {
		t.Errorf("bag = %v", bag)
	}
	if _, ok := bag["JITENV_TEST_ABSENT"]; ok {
		t.Errorf("absent var should not be imported")
	}
}

func TestBagImport_OnCollision_Overwrite(t *testing.T) {
	cfgPath := newImportTestConfig(t, map[string]map[string]string{
		"prod": {"A": "old", "KEEP": "untouched"},
	})
	out, _, err := runImport(t, "A=new\nB=2\n", "prod", "--stdin", "--on-collision=overwrite")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !strings.Contains(out, "1 new, 1 overwritten, 0 skipped") {
		t.Errorf("summary = %q", out)
	}
	bag := readBag(t, cfgPath, "prod")
	if bag["A"] != "new" || bag["B"] != "2" || bag["KEEP"] != "untouched" {
		t.Errorf("bag = %v", bag)
	}
}

func TestBagImport_OnCollision_Skip(t *testing.T) {
	cfgPath := newImportTestConfig(t, map[string]map[string]string{
		"prod": {"A": "old"},
	})
	out, _, err := runImport(t, "A=new\nB=2\n", "prod", "--stdin", "--on-collision=skip")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !strings.Contains(out, "1 new, 0 overwritten, 1 skipped") {
		t.Errorf("summary = %q", out)
	}
	bag := readBag(t, cfgPath, "prod")
	if bag["A"] != "old" || bag["B"] != "2" {
		t.Errorf("bag = %v", bag)
	}
}

func TestBagImport_ParseError_ConfigUntouched(t *testing.T) {
	cfgPath := newImportTestConfig(t, nil)
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	_, _, runErr := runImport(t, "GOOD=1\nthis is broken\n9BAD=x\n", "prod", "--stdin")
	if runErr == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(runErr.Error(), "parse error") {
		t.Errorf("err = %v", runErr)
	}
	if !strings.Contains(runErr.Error(), "line 2") {
		t.Errorf("expected line numbers in error, got %v", runErr)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("config must be untouched on parse error")
	}
}

func TestBagImport_DryRun_DoesNotWrite(t *testing.T) {
	cfgPath := newImportTestConfig(t, nil)
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := runImport(t, "A=1\n", "prod", "--stdin", "--dry-run")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !strings.Contains(out, "dry-run") || !strings.Contains(out, "not written") {
		t.Errorf("summary = %q", out)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("dry-run must not modify the config")
	}
}

func TestBagImport_MutuallyExclusiveSources(t *testing.T) {
	newImportTestConfig(t, nil)
	envFile := filepath.Join(t.TempDir(), ".env")
	_ = os.WriteFile(envFile, []byte("A=1\n"), 0o600)
	_, _, err := runImport(t, "", "prod", "--from-file", envFile, "--stdin")
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("want mutually-exclusive error, got %v", err)
	}
}

func TestBagImport_NoSource(t *testing.T) {
	newImportTestConfig(t, nil)
	_, _, err := runImport(t, "", "prod")
	if err == nil || !strings.Contains(err.Error(), "input source") {
		t.Fatalf("want no-source error, got %v", err)
	}
}

func TestBagImport_BadCollisionPolicy(t *testing.T) {
	newImportTestConfig(t, nil)
	_, _, err := runImport(t, "A=1\n", "prod", "--stdin", "--on-collision=nope")
	if err == nil || !strings.Contains(err.Error(), "on-collision") {
		t.Fatalf("want on-collision error, got %v", err)
	}
}

// TestBagImport_NewBagRoundTripsThroughIDLayer is the #248 interaction
// check: a brand-new bag with brand-new keys minted by import must
// survive a save → reload → decrypt cycle with values intact, proving
// the opaque-ID name_map sealed the freshly-minted IDs correctly.
func TestBagImport_NewBagRoundTripsThroughIDLayer(t *testing.T) {
	cfgPath := newImportTestConfig(t, nil)
	out, _, err := runImport(t, "TOKEN=s3cr3t\nDB_URL=postgres://x\n", "fresh", "--stdin")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !strings.Contains(out, "2 new") {
		t.Errorf("summary = %q", out)
	}
	// On-disk TOML must NOT contain the real names (they are opaque IDs).
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "TOKEN") || strings.Contains(string(raw), "fresh") {
		t.Errorf("real names leaked into on-disk TOML")
	}
	bag := readBag(t, cfgPath, "fresh")
	if bag["TOKEN"] != "s3cr3t" || bag["DB_URL"] != "postgres://x" {
		t.Errorf("round-trip = %v", bag)
	}
}

func TestBagUpsert_AskCallback(t *testing.T) {
	cfg := &config.Config{Secrets: map[string]map[string]string{
		"b": {"X": "old", "Y": "keep"},
	}}
	pairs := []importPair{{Key: "X", Value: "new"}, {Key: "Y", Value: "new2"}, {Key: "Z", Value: "z"}}
	asked := map[string]bool{}
	stats := bagUpsert(cfg, "b", pairs, collisionAsk, func(k string) bool {
		asked[k] = true
		return k == "X" // overwrite X, skip Y
	})
	if stats.Added != 1 || stats.Overwritten != 1 || stats.Skipped != 1 {
		t.Errorf("stats = %+v", stats)
	}
	if cfg.Secrets["b"]["X"] != "new" || cfg.Secrets["b"]["Y"] != "keep" || cfg.Secrets["b"]["Z"] != "z" {
		t.Errorf("bag = %v", cfg.Secrets["b"])
	}
	if asked["Z"] {
		t.Errorf("ask must not be called for non-colliding key Z")
	}
}

// newLegacyImportTestConfig writes a pre-#248 (name-keyed, name-AAD-sealed)
// config to a temp dir, seeds the named bag with a single colliding key,
// points JITENV_CONFIG at it, and returns the config path. This is the
// on-disk shape an older jitenv binary produced: bag NAMES are plaintext
// TOML map keys, values are envelopes bound to NAME-based AADs, and there
// is no _meta.name_map (so config.NeedsIDMigration == true).
func newLegacyImportTestConfig(t *testing.T, bag, key, val string) string {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	pw := []byte(testPassphrase)
	if err := config.InitNew(cfgPath, pw); err != nil {
		t.Fatalf("InitNew: %v", err)
	}
	c, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	mkey, err := config.DeriveKeyFromMeta(c, pw)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	t.Cleanup(func() {
		for i := range mkey {
			mkey[i] = 0
		}
	})
	// Seal the seed value under the LEGACY name-based AAD (the bag NAME is
	// the first coordinate, matching what a pre-#248 binary wrote).
	sealed, err := crypto.EncryptField(mkey, val, config.SecretAAD(bag, key))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	c.Sources = map[string]config.SourceConfig{"local": {Type: "local"}}
	c.Secrets = map[string]map[string]string{bag: {key: sealed}}
	// config.Save writes the struct verbatim (no re-encrypt / no ID minting),
	// preserving the legacy name-keyed shape on disk.
	if err := config.Save(cfgPath, c); err != nil {
		t.Fatalf("save legacy: %v", err)
	}
	// Sanity: this really is a legacy config that would trigger migration.
	reloaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !config.NeedsIDMigration(reloaded) {
		t.Fatal("fixture should be a legacy config needing migration")
	}
	t.Setenv("JITENV_CONFIG", cfgPath)
	return cfgPath
}

// TestBagImport_DryRun_LegacyConfig_NoSideEffects locks in the #254
// dry-run contract against a LEGACY (pre-#248) config: a dry-run must
// neither run the opaque-ID migration (which would rewrite the config and
// drop a .pre-id-migration.bak) nor write anything else. After the run the
// on-disk config.toml must be byte-identical and no backup may exist.
func TestBagImport_DryRun_LegacyConfig_NoSideEffects(t *testing.T) {
	cfgPath := newLegacyImportTestConfig(t, "prod", "A", "old")
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := runImport(t, "B=2\n", "prod", "--stdin", "--dry-run")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !strings.Contains(out, "dry-run") || !strings.Contains(out, "not written") {
		t.Errorf("summary = %q", out)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("dry-run must not modify a legacy config on disk")
	}
	if _, err := os.Stat(cfgPath + config.MigrationBackupSuffix); !os.IsNotExist(err) {
		t.Errorf("dry-run must not create a %s backup (stat err: %v)",
			config.MigrationBackupSuffix, err)
	}
}

// TestBagImport_DryRun_OnCollisionAsk_NoTTY locks in the #254 fix that
// --on-collision=ask under --dry-run does NOT read from the tty: the
// import must complete without any interactive prompt and report the
// colliding key as "would overwrite" in the summary. askOverwrite reads
// from /dev/tty, which is unavailable in tests, so if dry-run reached it
// the command would hang or error — proving the prompt is bypassed.
func TestBagImport_DryRun_OnCollisionAsk_NoTTY(t *testing.T) {
	cfgPath := newImportTestConfig(t, map[string]map[string]string{
		"prod": {"A": "old"},
	})
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	// A=new collides with the seeded A; B=2 is new. Under ask+dry-run the
	// collision is REPORTED as "would overwrite", with no tty interaction.
	out, _, err := runImport(t, "A=new\nB=2\n", "prod", "--stdin", "--on-collision=ask", "--dry-run")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !strings.Contains(out, "2 keys (1 new, 1 overwritten, 0 skipped)") {
		t.Errorf("dry-run ask summary = %q (want collision reported as would-overwrite)", out)
	}
	if !strings.Contains(out, "dry-run") || !strings.Contains(out, "not written") {
		t.Errorf("summary = %q", out)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("dry-run must not modify the config")
	}
}

// TestMigrationNoticeEmittedOnce verifies the #269 contract: when a
// legacy config is migrated to opaque-ID format during a key-holding
// command (here, bag import), the one-shot backup notice is printed to
// stderr exactly once — and a SECOND import (config already migrated)
// emits no notice, because the migration is idempotent. The notice must
// name the absolute backup path and warn that the backup holds secrets.
func TestMigrationNoticeEmittedOnce(t *testing.T) {
	cfgPath := newLegacyImportTestConfig(t, "prod", "A", "old")
	backup := cfgPath + config.MigrationBackupSuffix

	// First import triggers the migration → notice fires once.
	_, errOut, err := runImport(t, "B=2\n", "prod", "--stdin")
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	const marker = "upgraded config to opaque-ID format (#248)"
	if n := strings.Count(errOut, marker); n != 1 {
		t.Fatalf("migration notice should fire exactly once, got %d:\n%s", n, errOut)
	}
	if !strings.Contains(errOut, backup) {
		t.Errorf("notice must name the absolute backup path %q, got:\n%s", backup, errOut)
	}
	if !strings.Contains(errOut, "do not check it in or sync it") {
		t.Errorf("notice must warn the backup holds secrets, got:\n%s", errOut)
	}
	if _, statErr := os.Stat(backup); statErr != nil {
		t.Fatalf("backup must exist after migration: %v", statErr)
	}

	// Second import: config is already migrated, so no notice.
	_, errOut2, err := runImport(t, "C=3\n", "prod", "--stdin")
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if strings.Contains(errOut2, marker) {
		t.Errorf("migration notice must NOT re-fire on an already-migrated config, got:\n%s", errOut2)
	}
	// And the save from the second import must NOT have removed the backup (#269).
	if _, statErr := os.Stat(backup); statErr != nil {
		t.Fatalf("backup must survive subsequent saves (#269): %v", statErr)
	}
}
