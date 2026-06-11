package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/crypto"
	"github.com/gv/jitenv/internal/dotenv"
)

// newBagImportCmd implements `jitenv bag import <bag> [flags]` (#250) —
// a non-interactive, scriptable counterpart to the TUI bulk-import
// screen. It reads KEY=VALUE pairs from one of three sources (a .env
// file, named process-environment variables, or stdin), resolves
// collisions with any existing keys in the bag per --on-collision, and
// merges the result into an encrypted local bag.
func newBagImportCmd() *cobra.Command {
	var (
		fromFile    string
		fromEnv     []string
		fromStdin   bool
		onCollision string
		dryRun      bool
	)
	c := &cobra.Command{
		Use:   "import <bag>",
		Short: "Import KEY=VALUE pairs into an encrypted local bag.",
		Long: `Import KEY=VALUE pairs into a local secret bag, creating the bag if it does not exist.

Exactly one input source must be selected:

  --from-file PATH      read KEY=VALUE lines from a .env-style file
  --from-env VAR1,VAR2  copy the NAMED variables from this process's
                        environment into the bag, by name only
  --stdin               read KEY=VALUE lines from standard input

Collisions with keys already in the bag are resolved by --on-collision:

  ask        (default) prompt y/n per colliding key on the controlling
             terminal; requires a tty
  overwrite  replace existing values without asking (non-interactive)
  skip       keep existing values, drop the incoming ones (non-interactive)

SECURITY: --from-env is by NAME ONLY — there is deliberately no wildcard
or "import everything" mode. A hostile or misconfigured parent shell can
export arbitrary variables, and a wildcard import would silently seal
whatever happened to be in the environment (PATH, tokens belonging to
other tools, prompt-injection payloads) into your config. Name each
variable you intend to import.

Secret VALUES never appear on the command line. Use --stdin (or
--from-file) so values are not exposed in the process argument list or
shell history; --from-env reads values out of the environment, not argv.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := importOpts{
				bag:         args[0],
				fromFile:    fromFile,
				fromEnv:     fromEnv,
				fromStdin:   fromStdin,
				onCollision: onCollision,
				dryRun:      dryRun,
			}
			return runBagImport(cmd, opts)
		},
	}
	c.Flags().StringVar(&fromFile, "from-file", "", "read KEY=VALUE lines from a .env file")
	c.Flags().StringSliceVar(&fromEnv, "from-env", nil, "copy the named variables from the current environment (comma-separated; by name only)")
	c.Flags().BoolVar(&fromStdin, "stdin", false, "read KEY=VALUE lines from stdin")
	c.Flags().StringVar(&onCollision, "on-collision", "ask", "how to handle keys already in the bag: ask|overwrite|skip")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "parse and report what would change, without writing the config")
	return c
}

// importPassphraseFn is the passphrase reader bag import uses. It is a
// package var so tests can inject a fixed passphrase without a tty; in
// production it points at crypto.PromptPassphrase.
var importPassphraseFn = func() ([]byte, error) {
	return crypto.PromptPassphrase("jitenv bag import passphrase: ", false)
}

type importOpts struct {
	bag         string
	fromFile    string
	fromEnv     []string
	fromStdin   bool
	onCollision string
	dryRun      bool
}

func runBagImport(cmd *cobra.Command, opts importOpts) error {
	// Resolve the collision policy first — a typo here should fail before
	// we prompt for a passphrase or read any secrets.
	policy, err := parseCollisionPolicy(opts.onCollision)
	if err != nil {
		return err
	}

	// Exactly one input source. Combining them is a usage error.
	pairs, err := gatherImportPairs(cmd, opts)
	if err != nil {
		return err
	}
	if len(pairs) == 0 {
		return errors.New("nothing to import — no KEY=VALUE pairs found in the input")
	}

	// Load + decrypt the config (mirrors `jitenv clone`).
	cfgPath, err := config.Resolve(os.Getenv("JITENV_CONFIG"))
	if err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load %s: %w (run `jitenv config init` first)", cfgPath, err)
	}
	pw, err := importPassphraseFn()
	if err != nil {
		return err
	}
	defer zeroBytes(pw)
	key, err := config.DeriveKeyFromMeta(cfg, pw)
	if err != nil {
		return err
	}
	defer zeroBytes(key)
	defer lockKey(key)()

	// Dry-run must be byte-for-byte side-effect-free: no opaque-ID
	// migration (which would write the migrated config + a dated
	// *.pre-id-migration.*.bak, #304), no save, and no TTY prompts. We
	// decrypt the config IN MEMORY ONLY to read the target bag's existing
	// keys, then report what WOULD change. A legacy (pre-#248) config decrypts under
	// its name-based AADs into a name-keyed cfg.Secrets — exactly the
	// shape bagUpsert's collision check needs — so no in-memory migration
	// is required for the report either.
	if opts.dryRun {
		if err := config.DecryptInPlace(cfg, key); err != nil {
			return err
		}
		// Under --on-collision=ask, dry-run must NOT prompt: report every
		// collision as "would overwrite" instead of reading from the tty.
		stats := bagUpsert(cfg, opts.bag, pairs, policy, func(string) bool { return true })
		fmt.Fprintf(cmd.OutOrStdout(),
			"dry-run: would import %d keys (%d new, %d overwritten, %d skipped) into bag %q (config not written)\n",
			stats.Total(), stats.Added, stats.Overwritten, stats.Skipped, opts.bag)
		return nil
	}

	// One-shot opaque-ID migration (#248) so a legacy config gets the
	// sealed name_map + backup before we mint a new bag/keys into it,
	// matching the clone/unlock paths. The lock against `jitenv config`
	// and concurrent migrations is taken internally by
	// config.MigrateToOpaqueIDs (#275 hoist).
	migrated, err := config.MigrateToOpaqueIDs(cfgPath, key)
	if err != nil {
		return err
	}
	if migrated {
		printMigrationNotice(cmd.ErrOrStderr(), cfgPath)
	}
	cfg, err = config.Load(cfgPath)
	if err != nil {
		return err
	}
	if err := config.DecryptInPlace(cfg, key); err != nil {
		return err
	}

	// Merge through the shared upsert path so collision handling matches
	// clone. The ask callback is only consulted under --on-collision=ask.
	stats := bagUpsert(cfg, opts.bag, pairs, policy, func(k string) bool {
		return askOverwrite(cmd, opts.bag, k)
	})

	// Ensure a local source exists to expose the bag (mirrors clone).
	if cfg.Sources == nil {
		cfg.Sources = map[string]config.SourceConfig{}
	}
	if _, ok := cfg.Sources[localSourceName(cfg)]; !ok {
		cfg.Sources["local"] = config.SourceConfig{Type: "local"}
	}

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("internal: config invalid after import: %w", err)
	}
	// EncryptInPlace mints opaque IDs for the freshly-created bag / keys
	// (#248) and re-seals every value before AtomicSave.
	if err := saveAndReencrypt(cfgPath, cfg, key); err != nil {
		return err
	}
	pingAgentReloadFromClone()

	fmt.Fprintf(cmd.OutOrStdout(),
		"imported %d keys (%d new, %d overwritten, %d skipped) into bag %q\n",
		stats.Total(), stats.Added, stats.Overwritten, stats.Skipped, opts.bag)
	return nil
}

// gatherImportPairs enforces the exactly-one-source rule and returns the
// deduplicated pairs from whichever source was selected.
func gatherImportPairs(cmd *cobra.Command, opts importOpts) ([]importPair, error) {
	n := 0
	if opts.fromFile != "" {
		n++
	}
	if len(opts.fromEnv) > 0 {
		n++
	}
	if opts.fromStdin {
		n++
	}
	switch {
	case n == 0:
		return nil, errors.New("choose an input source: one of --from-file, --from-env, or --stdin")
	case n > 1:
		return nil, errors.New("--from-file, --from-env, and --stdin are mutually exclusive; pick one")
	}

	switch {
	case opts.fromFile != "":
		data, err := os.ReadFile(opts.fromFile)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", opts.fromFile, err)
		}
		return parseDotenvPairs(string(data))
	case opts.fromStdin:
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
		return parseDotenvPairs(string(data))
	default:
		return pairsFromEnv(cmd, opts.fromEnv)
	}
}

// parseDotenvPairs runs the shared parser and, on any parse error,
// returns a single error listing every offending line so the caller can
// exit non-zero WITHOUT touching the config.
func parseDotenvPairs(input string) ([]importPair, error) {
	pairs, perrs := dotenv.Parse(input)
	if len(perrs) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "%d parse error(s):", len(perrs))
		for _, e := range perrs {
			b.WriteString("\n  " + e.Error())
		}
		return nil, errors.New(b.String())
	}
	return dedupePairs(pairs), nil
}

// pairsFromEnv reads each named variable out of the current process
// environment. Names are validated and de-duplicated; a name that is
// not set in the environment is reported as a warning to stderr and
// skipped (so `--from-env A,B` still imports A when B is unset). A name
// that is not a valid env identifier is a hard error.
func pairsFromEnv(cmd *cobra.Command, names []string) ([]importPair, error) {
	seen := map[string]struct{}{}
	var pairs []importPair
	var missing []string
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if !isValidEnvName(name) {
			return nil, fmt.Errorf("--from-env: %q is not a valid environment variable name", name)
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		val, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)
			continue
		}
		pairs = append(pairs, importPair{Key: name, Value: val})
	}
	if len(missing) > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"warning: not set in environment, skipped: %s\n", strings.Join(missing, ", "))
	}
	return pairs, nil
}

// dedupePairs collapses duplicate keys keeping the LAST occurrence,
// matching `set -a; source .env` semantics and the TUI bulk-import
// behaviour.
func dedupePairs(pairs []dotenv.Pair) []importPair {
	lastIdx := make(map[string]int, len(pairs))
	for i, p := range pairs {
		lastIdx[p.Key] = i
	}
	out := make([]importPair, 0, len(lastIdx))
	for i, p := range pairs {
		if lastIdx[p.Key] == i {
			out = append(out, importPair{Key: p.Key, Value: p.Value})
		}
	}
	return out
}

// askOverwrite prompts y/n on the controlling terminal for a single
// colliding key. Talks to /dev/tty (not stdin) so it works even when
// the import data is piped in on stdin. Anything but an affirmative
// answer is treated as "skip".
func askOverwrite(cmd *cobra.Command, bag, key string) bool {
	line, err := crypto.PromptTTYLine(fmt.Sprintf("bag %q already has %q — overwrite? [y/N] ", bag, key))
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "no terminal for collision prompt (%v) — skipping %q\n", err, key)
		return false
	}
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

func parseCollisionPolicy(s string) (collisionPolicy, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "ask", "":
		return collisionAsk, nil
	case "overwrite":
		return collisionOverwrite, nil
	case "skip":
		return collisionSkip, nil
	default:
		return collisionAsk, fmt.Errorf("--on-collision: %q is not one of ask|overwrite|skip", s)
	}
}

// isValidEnvName mirrors the conservative key rule the dotenv parser
// enforces: start with a letter/underscore, then letters/digits/_.
func isValidEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_':
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}
