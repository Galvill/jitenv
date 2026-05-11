// Command seed generates encrypted jitenv config.toml fixtures for the
// e2e harness. It is intended to run inside the test container as the
// non-root user, so the resulting file lives at the path the agent
// will later resolve via $JITENV_CONFIG.
//
// We can't shell out to `jitenv config init` here: the CLI prompts for
// a passphrase via /dev/tty, which doesn't work non-interactively. So
// we drive the same internal APIs the TUI uses (config.InitNew +
// crypto.EncryptField) and write the file directly.
//
// Fixture variants are selected with -variant:
//
//   - local        a local-bag-only fixture: sources.vault (local) +
//     secrets.demo containing FOO/BAR; mapping on
//     /home/jitenv/scripts/show.sh expands the bag.
//   - local-alt    same shape as local but the bag values are
//     suffixed with "-v2" so a reload scenario can tell
//     them apart after re-seeding.
//   - local-glob   like local but the mapping uses Glob instead of
//     Path (defaults to /home/jitenv/scripts/*.sh) and
//     the bag carries FOO/BAR/BAZ to exercise full bag
//     expansion via a single empty-Name VarRef.
//   - localstack   an aws-source fixture pointing at LocalStack with
//     static dummy creds; mapping on .../show.sh fetches
//     one key from the seeded SM secret.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/crypto"
)

func main() {
	var (
		out          = flag.String("out", "", "output path for config.toml (required)")
		passphrase   = flag.String("passphrase", "e2e-test-pass", "passphrase for the encrypted config")
		variant      = flag.String("variant", "local", "fixture variant: local | local-alt | local-glob | localstack")
		scriptPath   = flag.String("script", "/home/jitenv/scripts/show.sh", "absolute path used in the mapping (path variants)")
		globPath     = flag.String("glob", "/home/jitenv/scripts/*.sh", "glob pattern used in the mapping (local-glob variant)")
		smARN        = flag.String("sm-arn", "arn:aws:secretsmanager:us-east-1:000000000000:secret:jitenv/demo", "SM secret ARN (localstack variant)")
		smEndpoint   = flag.String("sm-endpoint", "http://localstack:4566", "SM endpoint URL (localstack variant)")
		preserveMeta = flag.Bool("preserve-meta", false, "preserve the existing config's Meta (salt + verify sentinel) so a running agent's derived key still decrypts the new file")
	)
	flag.Parse()

	if *out == "" {
		fmt.Fprintln(os.Stderr, "seed: -out is required")
		os.Exit(2)
	}
	if err := run(*out, []byte(*passphrase), *variant, *scriptPath, *globPath, *smARN, *smEndpoint, *preserveMeta); err != nil {
		fmt.Fprintf(os.Stderr, "seed: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stdout, "wrote %s (variant=%s)\n", *out, *variant)
}

func run(out string, pw []byte, variant, scriptPath, globPath, smARN, smEndpoint string, preserveMeta bool) error {
	if err := os.MkdirAll(dirOf(out), 0700); err != nil {
		return fmt.Errorf("mkdir parent: %w", err)
	}
	// preserveMeta keeps the existing file's Meta (salt + verify
	// sentinel) when reseeding. Without it, every reseed picks a new
	// salt and any running agent — whose key was derived from the
	// previous salt — can no longer decrypt the file. The
	// mid-session-reload scenario depends on this: it reseeds while
	// the agent is up and then pings OpReload.
	var cfg *config.Config
	if preserveMeta {
		existing, err := config.Load(out)
		if err != nil {
			return fmt.Errorf("load existing config (preserve-meta): %w", err)
		}
		// Verify the passphrase derives the same key, otherwise the
		// agent would have rejected the new contents anyway.
		if _, err := config.DeriveKeyFromMeta(existing, pw); err != nil {
			return fmt.Errorf("verify passphrase against existing meta: %w", err)
		}
		cfg = &config.Config{Version: existing.Version, Meta: existing.Meta, Agent: existing.Agent}
	} else {
		if _, err := os.Stat(out); err == nil {
			if err := os.Remove(out); err != nil {
				return fmt.Errorf("remove existing config: %w", err)
			}
		}
		if err := config.InitNew(out, pw); err != nil {
			return fmt.Errorf("init config: %w", err)
		}
		loaded, err := config.Load(out)
		if err != nil {
			return fmt.Errorf("reload config: %w", err)
		}
		cfg = loaded
	}
	key, err := config.DeriveKeyFromMeta(cfg, pw)
	if err != nil {
		return fmt.Errorf("derive key: %w", err)
	}
	defer zero(key)

	switch variant {
	case "local":
		if err := applyLocal(cfg, key, scriptPath, "value-from-local-foo", "value-from-local-bar"); err != nil {
			return err
		}
	case "local-alt":
		if err := applyLocal(cfg, key, scriptPath, "value-from-local-foo-v2", "value-from-local-bar-v2"); err != nil {
			return err
		}
	case "local-glob":
		if err := applyLocalGlob(cfg, key, globPath); err != nil {
			return err
		}
	case "localstack":
		if err := applyLocalstack(cfg, key, scriptPath, smARN, smEndpoint); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown variant %q (want: local | local-alt | local-glob | localstack)", variant)
	}

	return config.AtomicSave(out, cfg)
}

// applyLocal mirrors what the TUI emits for a local-bag-only setup:
// one source of type "local", one secrets table, one mapping that
// expands the bag. fooVal/barVal let the caller produce distinguishable
// fixtures (used by local-alt to differentiate before/after a reload).
func applyLocal(cfg *config.Config, key []byte, scriptPath, fooVal, barVal string) error {
	foo, err := crypto.EncryptField(key, fooVal)
	if err != nil {
		return err
	}
	bar, err := crypto.EncryptField(key, barVal)
	if err != nil {
		return err
	}
	cfg.Sources = map[string]config.SourceConfig{
		"vault": {Type: "local"},
	}
	cfg.Secrets = map[string]map[string]string{
		"demo": {"FOO": foo, "BAR": bar},
	}
	cfg.Mappings = []config.Mapping{
		{
			Path: scriptPath,
			Vars: []config.VarRef{
				// Empty Name + empty Key = expand all keys in the bag.
				{Source: "vault", Ref: "demo"},
			},
		},
	}
	return nil
}

// applyLocalGlob covers two features in one fixture: a Glob mapping
// (instead of an exact Path) and a bag with three keys that all expand
// via a single empty-Name VarRef.
func applyLocalGlob(cfg *config.Config, key []byte, globPath string) error {
	foo, err := crypto.EncryptField(key, "value-from-local-foo")
	if err != nil {
		return err
	}
	bar, err := crypto.EncryptField(key, "value-from-local-bar")
	if err != nil {
		return err
	}
	baz, err := crypto.EncryptField(key, "value-from-local-baz")
	if err != nil {
		return err
	}
	cfg.Sources = map[string]config.SourceConfig{
		"vault": {Type: "local"},
	}
	cfg.Secrets = map[string]map[string]string{
		"demo": {"FOO": foo, "BAR": bar, "BAZ": baz},
	}
	cfg.Mappings = []config.Mapping{
		{
			Glob: globPath,
			Vars: []config.VarRef{
				{Source: "vault", Ref: "demo"},
			},
		},
	}
	return nil
}

// applyLocalstack wires an aws Secrets Manager source at the LocalStack
// endpoint. Static creds are used because LocalStack accepts any string.
// We only fetch a single JSON key (FOO) to also exercise that path.
func applyLocalstack(cfg *config.Config, key []byte, scriptPath, smARN, smEndpoint string) error {
	akid, err := crypto.EncryptField(key, "test")
	if err != nil {
		return err
	}
	sak, err := crypto.EncryptField(key, "test")
	if err != nil {
		return err
	}
	cfg.Sources = map[string]config.SourceConfig{
		"awssm": {
			Type: "aws",
			Params: map[string]any{
				"region":            "us-east-1",
				"access_key_id":     akid,
				"secret_access_key": sak,
				"endpoint_override": smEndpoint,
			},
		},
	}
	cfg.Mappings = []config.Mapping{
		{
			Path: scriptPath,
			Vars: []config.VarRef{
				{Name: "FOO", Source: "awssm", Ref: smARN, Key: "FOO"},
			},
		},
	}
	return nil
}

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
