package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gv/jitenv/internal/config"
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
