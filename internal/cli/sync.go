package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/syncadapters"
	"github.com/gv/jitenv/internal/syncconfig"
	"github.com/gv/jitenv/internal/unlock"
	"github.com/gv/jitenv/pkg/syncadapter"

	// Blank-import the builtins so the registry is populated even in the
	// (unlikely) event the binary linker drops the cmd/jitenv import.
	_ "github.com/gv/jitenv/internal/syncadapters/builtin"
)

var syncConfigPath string

// newSyncCmd is the top-level `jitenv sync` subtree (#241). Config sync
// pushes the encrypted config.toml to a pluggable remote adapter and
// pulls it back with a last-writer-wins merge plus a divergence fence.
// The remote only ever sees one opaque AEAD blob — never plaintext.
func newSyncCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "sync",
		Short: "Sync the encrypted config to a pluggable remote (SSH, file).",
		Long: `Push and pull the jitenv config to/from a remote adapter.

The config.toml is sealed under a per-config data key (itself wrapped by
your passphrase) before it leaves the host, so the remote stores only an
opaque ciphertext blob — never plaintext, never your secrets.

Merge model (v1): whole-file last-writer-wins with a divergence fence.
'pull' fast-forwards when only the remote moved; if BOTH the local and
remote config changed since the last sync, 'pull' aborts and asks you to
reconcile manually (see 'jitenv sync status').`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	c.PersistentFlags().StringVar(&syncConfigPath, "sync-config", "", "path to the sync sidecar (default: $JITENV_CONFIG_SYNC or sync.toml next to config.toml)")
	c.AddCommand(newSyncInitCmd())
	c.AddCommand(newSyncPushCmd())
	c.AddCommand(newSyncPullCmd())
	c.AddCommand(newSyncStatusCmd())
	c.AddCommand(newSyncListCmd())
	return c
}

// loadSidecar resolves and loads the sync sidecar, returning a helpful
// error if it doesn't exist yet.
func loadSidecar() (string, *syncconfig.File, error) {
	path, err := config.ResolveSync(syncConfigPath)
	if err != nil {
		return "", nil, err
	}
	f, err := syncconfig.Load(path)
	if errors.Is(err, os.ErrNotExist) {
		return path, nil, fmt.Errorf("no sync sidecar at %s; run `jitenv sync init` first", path)
	}
	if err != nil {
		return path, nil, err
	}
	return path, f, nil
}

// readConfigBytes reads the raw config.toml bytes (the exact bytes that
// get sealed and synced). Returns the resolved path too.
func readConfigBytes() (string, []byte, error) {
	cfgPath, err := config.Resolve(configPath)
	if err != nil {
		return "", nil, err
	}
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		return cfgPath, nil, fmt.Errorf("read %s: %w (run `jitenv config init` first)", cfgPath, err)
	}
	return cfgPath, b, nil
}

// ---------------------------------------------------------------------
// sync init
// ---------------------------------------------------------------------

func newSyncInitCmd() *cobra.Command {
	var adapterType, adapterName string
	var params []string
	c := &cobra.Command{
		Use:   "init",
		Short: "Create the sync sidecar and configure a remote adapter.",
		Long: `Create the local sync sidecar (sync.toml) and register one remote
adapter. The sidecar stores the per-config data-encryption key wrapped by
your passphrase, plus the adapter's connection parameters (sensitive
values encrypted on disk).

Adapter params are passed as --param key=value (repeatable). Required
params per type:
  file   path=/abs/path/to/blob
  ssh    host=user@host path=/abs/remote/path [port=22]`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncInit(cmd, adapterType, adapterName, params)
		},
	}
	c.Flags().StringVar(&adapterType, "type", "", "adapter type ("+strings.Join(syncadapters.Types(), ", ")+")")
	c.Flags().StringVar(&adapterName, "name", "", "label for this adapter (defaults to the type)")
	c.Flags().StringArrayVar(&params, "param", nil, "adapter param as key=value (repeatable)")
	return c
}

func runSyncInit(cmd *cobra.Command, adapterType, adapterName string, params []string) error {
	out := cmd.OutOrStdout()

	if adapterType == "" {
		return fmt.Errorf("--type is required (one of: %s)", strings.Join(syncadapters.Types(), ", "))
	}
	known := false
	for _, t := range syncadapters.Types() {
		if t == adapterType {
			known = true
		}
	}
	if !known {
		return fmt.Errorf("unknown adapter type %q (known: %s)", adapterType, strings.Join(syncadapters.Types(), ", "))
	}
	if adapterName == "" {
		adapterName = adapterType
	}

	paramMap, err := parseParams(params)
	if err != nil {
		return err
	}

	// We need the data config to copy its KDF salt/params so the master
	// key derived from the sync sidecar matches the one that unlocks
	// config.toml. Then prompt the passphrase and derive the key.
	cfgPath, err := config.Resolve(configPath)
	if err != nil {
		return err
	}
	dataCfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load %s: %w (run `jitenv config init` first)", cfgPath, err)
	}
	if dataCfg.Meta.Salt == "" {
		return fmt.Errorf("%s has no _meta header; run `jitenv config init` first", cfgPath)
	}

	// Verify the passphrase against the data config before writing
	// anything, and reuse the derived master key to wrap the DEK. The
	// bounded retry (#326) lets the user fix a typo without re-running
	// `jitenv sync init` from scratch.
	masterKey, err := unlock.PromptAndDeriveKey(dataCfg, "jitenv sync init: passphrase: ", 0)
	if err != nil {
		return err
	}
	defer zeroBytes(masterKey)

	syncPath, err := config.ResolveSync(syncConfigPath)
	if err != nil {
		return err
	}

	// If a sidecar already exists, append the adapter to it (reusing the
	// existing wrapped DEK); otherwise create a fresh one.
	var f *syncconfig.File
	if existing, lerr := syncconfig.Load(syncPath); lerr == nil {
		f = existing
		if _, _, ok := f.FindAdapter(adapterName); ok {
			return fmt.Errorf("adapter %q already exists in %s", adapterName, syncPath)
		}
	} else if errors.Is(lerr, os.ErrNotExist) {
		f = &syncconfig.File{
			Version:        syncconfig.Version,
			Salt:           dataCfg.Meta.Salt,
			ArgonTime:      dataCfg.Meta.ArgonTime,
			ArgonMemoryKiB: dataCfg.Meta.ArgonMemoryKiB,
			ArgonThreads:   dataCfg.Meta.ArgonThreads,
		}
		dek, derr := syncconfig.NewDEK()
		if derr != nil {
			return derr
		}
		defer zeroBytes(dek)
		if werr := f.WrapDEK(masterKey, dek); werr != nil {
			return werr
		}
	} else {
		return lerr
	}

	adapter := syncconfig.Adapter{Name: adapterName, Type: adapterType, Params: paramMap}

	// Build + Validate the adapter against decrypted params (paramMap is
	// still plaintext here) before persisting, so a bad host/path fails
	// fast.
	built, err := syncadapters.Build(adapterType, paramMap)
	if err != nil {
		return err
	}
	if err := built.Validate(context.Background()); err != nil {
		return fmt.Errorf("adapter validation failed: %w", err)
	}

	// Encrypt the adapter params in place before save.
	if err := syncconfig.EncryptParams(masterKey, &adapter); err != nil {
		return err
	}
	f.Adapters = append(f.Adapters, adapter)

	if err := syncconfig.Save(syncPath, f); err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote %s\nconfigured adapter %q (type %s)\n", syncPath, adapterName, adapterType)
	fmt.Fprintf(out, "run `jitenv sync push` to publish your config.\n")
	return nil
}

// ---------------------------------------------------------------------
// sync push
// ---------------------------------------------------------------------

func newSyncPushCmd() *cobra.Command {
	var adapterName string
	var force bool
	c := &cobra.Command{
		Use:   "push",
		Short: "Encrypt and push the local config to a remote adapter.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncPush(cmd, adapterName, force)
		},
	}
	c.Flags().StringVar(&adapterName, "adapter", "", "adapter name (defaults to the only configured adapter)")
	c.Flags().BoolVar(&force, "force", false, "push even if the remote advanced past our base snapshot (overwrites remote)")
	return c
}

func runSyncPush(cmd *cobra.Command, adapterName string, force bool) error {
	out := cmd.OutOrStdout()
	syncPath, f, err := loadSidecar()
	if err != nil {
		return err
	}
	ad, _, err := pickAdapter(f, adapterName)
	if err != nil {
		return err
	}

	_, cfgBytes, err := readConfigBytes()
	if err != nil {
		return err
	}

	// Bounded retry (#326): f.DeriveMasterKey by itself can't verify the
	// passphrase (no Meta.Verify on the sidecar), so the unwrap of the
	// wrapped DEK IS the verifier. Pair them inside PromptWithRetry so
	// a typo re-prompts instead of bailing the whole `sync push`.
	masterKey, dek, err := promptAndUnlockSync(f, "jitenv sync push: passphrase: ")
	if err != nil {
		return err
	}
	defer zeroBytes(masterKey)
	defer zeroBytes(dek)

	adapter, err := buildAdapter(masterKey, ad)
	if err != nil {
		return err
	}

	res, err := syncconfig.PushConfig(context.Background(), adapter, ad, dek, cfgBytes, config.Version, force)
	if err != nil {
		return err
	}
	if err := syncconfig.Save(syncPath, f); err != nil {
		return err
	}
	fmt.Fprintf(out, "pushed config (%s) to adapter %q\n", short(res.Hash), ad.Name)
	return nil
}

// ---------------------------------------------------------------------
// sync pull
// ---------------------------------------------------------------------

func newSyncPullCmd() *cobra.Command {
	var adapterName string
	var adopt bool
	c := &cobra.Command{
		Use:   "pull",
		Short: "Pull the remote config and merge (last-writer-wins) into local.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncPull(cmd, adapterName, adopt)
		},
	}
	c.Flags().StringVar(&adapterName, "adapter", "", "adapter name (defaults to the only configured adapter)")
	c.Flags().BoolVar(&adopt, "adopt", false, "first pull on a new machine: take the remote as authoritative when no local base snapshot exists")
	return c
}

func runSyncPull(cmd *cobra.Command, adapterName string, adopt bool) error {
	out := cmd.OutOrStdout()
	syncPath, f, err := loadSidecar()
	if err != nil {
		return err
	}
	ad, _, err := pickAdapter(f, adapterName)
	if err != nil {
		return err
	}

	cfgPath, cfgBytes, err := readConfigBytes()
	if err != nil {
		return err
	}

	masterKey, dek, err := promptAndUnlockSync(f, "jitenv sync pull: passphrase: ")
	if err != nil {
		return err
	}
	defer zeroBytes(masterKey)
	defer zeroBytes(dek)

	adapter, err := buildAdapter(masterKey, ad)
	if err != nil {
		return err
	}

	res, err := syncconfig.PullConfig(context.Background(), adapter, ad, dek, cfgBytes, adopt)
	var div *syncconfig.DivergenceError
	if errors.As(err, &div) {
		// Local config is left untouched on divergence.
		return fmt.Errorf("%w\nlocal config left untouched. reconcile manually: review both, edit via `jitenv config`, then publish the version you want with `jitenv sync push --force`", div)
	}
	if err != nil {
		return err
	}

	switch res.Decision {
	case syncconfig.DecideNoRemote:
		return errors.New("remote has no config yet; nothing to pull (run `jitenv sync push` to publish)")
	case syncconfig.DecidePushNeeded:
		fmt.Fprintf(out, "local is ahead of remote; nothing to pull. run `jitenv sync push` to publish.\n")
		return nil
	case syncconfig.DecideNoop:
		if err := syncconfig.Save(syncPath, f); err != nil { // persist the base anchor
			return fmt.Errorf("save sync sidecar: %w", err)
		}
		fmt.Fprintf(out, "already up-to-date (%s)\n", short(res.Hash))
		return nil
	case syncconfig.DecideFastForward:
		if err := writePulledConfig(cfgPath, res.Applied); err != nil {
			return err
		}
		if err := syncconfig.Save(syncPath, f); err != nil {
			return err
		}
		pingAgentReloadFromClone()
		fmt.Fprintf(out, "pulled and applied remote config (%s)\n", short(res.Hash))
		return nil
	default:
		return fmt.Errorf("internal: unexpected merge decision %v", res.Decision)
	}
}

// writePulledConfig validates the pulled bytes parse + ValidateStructure,
// then writes them through config.AtomicSave so the agent reload hook
// fires and on-disk perms stay 0600.
//
// Pulled bytes are the raw on-disk (encrypted) form (sync pushes the
// bytes returned by readConfigBytes). After #248 var.source on disk is
// an enc:v2:... envelope, so the full Validate() — which calls
// ValidatePost() and cross-references var.source against the s_xxxxxx-
// keyed Sources map — spuriously rejects every realistic pulled config.
// Use ValidateStructure(), the encrypted-form-safe variant whose doc
// comment explicitly directs callers operating on the encrypted form to
// use it.
func writePulledConfig(cfgPath string, plaintext []byte) error {
	// Stage the decrypted plaintext in the config directory (which is
	// 0700), not os.TempDir() which is world-traversable — mirrors
	// config.AtomicSave's sibling-tempfile approach.
	tmp, err := os.CreateTemp(filepath.Dir(cfgPath), "jitenv-pull-*.toml")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := os.Chmod(tmpName, 0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(plaintext); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	parsed, err := config.Load(tmpName)
	if err != nil {
		return fmt.Errorf("pulled config does not parse: %w", err)
	}
	if err := parsed.ValidateStructure(); err != nil {
		return fmt.Errorf("pulled config is invalid, refusing to apply: %w", err)
	}
	return config.AtomicSave(cfgPath, parsed)
}

// ---------------------------------------------------------------------
// sync status
// ---------------------------------------------------------------------

func newSyncStatusCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "status",
		Short: "Show local-vs-remote divergence for each adapter.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncStatus(cmd)
		},
	}
	return c
}

func runSyncStatus(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	_, f, err := loadSidecar()
	if err != nil {
		return err
	}
	if len(f.Adapters) == 0 {
		fmt.Fprintln(out, "no adapters configured")
		return nil
	}
	_, cfgBytes, err := readConfigBytes()
	if err != nil {
		return err
	}
	localHash := syncconfig.HashConfig(cfgBytes)

	// `sync status` needs the master key to decrypt adapter params
	// inside buildAdapter, but never touches the blob — so the DEK is
	// only used here as a passphrase verifier (so a typo retries
	// immediately instead of producing N per-adapter "decrypt failed"
	// lines later in the loop, #326). Zero the DEK as soon as we have
	// the master key.
	masterKey, dek, err := promptAndUnlockSync(f, "jitenv sync status: passphrase: ")
	if err != nil {
		return err
	}
	zeroBytes(dek)
	defer zeroBytes(masterKey)

	fmt.Fprintln(out, "merge model: whole-file last-writer-wins with a divergence fence")
	fmt.Fprintf(out, "local config: %s\n\n", short(localHash))
	for i := range f.Adapters {
		ad := &f.Adapters[i]
		adapter, berr := buildAdapter(masterKey, ad)
		if berr != nil {
			fmt.Fprintf(out, "  %-16s [%s]  error: %v\n", ad.Name, ad.Type, berr)
			continue
		}
		_, rmeta, perr := adapter.Pull(context.Background())
		remotePresent := true
		remoteShort := "-"
		switch {
		case errors.Is(perr, syncadapters.ErrNoRemoteState):
			remotePresent = false
		case errors.Is(perr, syncadapters.ErrRemoteStateIncomplete):
			// Surface the orphan-blob case explicitly: status would
			// otherwise read as "diverged" against a zero-hash remote
			// which is more confusing than helpful (#279).
			fmt.Fprintf(out, "  %-16s [%s]  remote state is incomplete (blob or meta missing); re-publish with `jitenv sync push --force` to overwrite\n", ad.Name, ad.Type)
			continue
		case perr != nil:
			fmt.Fprintf(out, "  %-16s [%s]  unreachable: %v\n", ad.Name, ad.Type, perr)
			continue
		default:
			remoteShort = short(rmeta.Hash)
		}
		d := syncconfig.Decide(localHash, rmeta.Hash, ad.BaseHash, remotePresent)
		fmt.Fprintf(out, "  %-16s [%s]  remote=%s base=%s  %s\n", ad.Name, ad.Type, remoteShort, short(ad.BaseHash), d)
	}
	return nil
}

// ---------------------------------------------------------------------
// sync list
// ---------------------------------------------------------------------

func newSyncListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "list",
		Short: "List configured sync adapters.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			_, f, err := loadSidecar()
			if err != nil {
				return err
			}
			if len(f.Adapters) == 0 {
				fmt.Fprintln(out, "no adapters configured")
				return nil
			}
			for _, ad := range f.Adapters {
				base := short(ad.BaseHash)
				fmt.Fprintf(out, "  %-16s [%s]  base=%s\n", ad.Name, ad.Type, base)
			}
			return nil
		},
	}
	return c
}

// ---------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------

// pickAdapter resolves the adapter to operate on: the named one, or the
// sole configured adapter when no name is given.
func pickAdapter(f *syncconfig.File, name string) (*syncconfig.Adapter, int, error) {
	if name != "" {
		ad, i, ok := f.FindAdapter(name)
		if !ok {
			return nil, -1, fmt.Errorf("no adapter named %q (configured: %s)", name, adapterNames(f))
		}
		return ad, i, nil
	}
	switch len(f.Adapters) {
	case 0:
		return nil, -1, errors.New("no adapters configured; run `jitenv sync init`")
	case 1:
		return &f.Adapters[0], 0, nil
	default:
		return nil, -1, fmt.Errorf("multiple adapters configured (%s); pass --adapter", adapterNames(f))
	}
}

func adapterNames(f *syncconfig.File) string {
	names := make([]string, 0, len(f.Adapters))
	for _, a := range f.Adapters {
		names = append(names, a.Name)
	}
	return strings.Join(names, ", ")
}

// buildAdapter decrypts the adapter's params under masterKey and
// constructs the concrete adapter.
func buildAdapter(masterKey []byte, ad *syncconfig.Adapter) (syncadapter.Adapter, error) {
	params, err := syncconfig.DecryptParams(masterKey, ad)
	if err != nil {
		return nil, fmt.Errorf("decrypt adapter %q params: %w", ad.Name, err)
	}
	return syncadapters.Build(ad.Type, params)
}

// parseParams turns ["k=v", ...] into a map. Empty list -> empty map.
func parseParams(kvs []string) (map[string]any, error) {
	m := map[string]any{}
	for _, kv := range kvs {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			return nil, fmt.Errorf("invalid --param %q (want key=value)", kv)
		}
		m[kv[:i]] = kv[i+1:]
	}
	return m, nil
}

// promptAndUnlockSync drives the sync passphrase prompt through the
// bounded-retry helper (#326). The master key for a sync sidecar is not
// directly verifiable (sync.toml has no Meta.Verify of its own), so the
// unwrap of the wrapped DEK doubles as the wrong-passphrase check:
// syncconfig.UnwrapDEK returns syncconfig.ErrIncorrectPassphrase on
// AEAD failure, which the retry loop recognises via errors.Is.
//
// Returns (masterKey, dek). Both buffers are the caller's to zero. On
// any error nothing is returned and the helper has already zeroed any
// transient key material.
func promptAndUnlockSync(f *syncconfig.File, prompt string) ([]byte, []byte, error) {
	var dek []byte
	masterKey, err := unlock.PromptWithRetry(prompt, 0, func(pw []byte) ([]byte, error) {
		mk, derr := f.DeriveMasterKey(pw)
		if derr != nil {
			return nil, derr
		}
		d, uerr := f.UnwrapDEK(mk)
		if uerr != nil {
			// Zero the transient master key on the wrong-passphrase
			// path so the loop's between-attempts heap is bounded
			// (CLAUDE.md "Master key handling").
			zeroBytes(mk)
			return nil, uerr
		}
		dek = d
		return mk, nil
	}, func(err error) bool {
		return errors.Is(err, syncconfig.ErrIncorrectPassphrase)
	})
	if err != nil {
		return nil, nil, err
	}
	return masterKey, dek, nil
}

// short truncates a hex hash for display; empty stays "-".
func short(h string) string {
	if h == "" {
		return "-"
	}
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
