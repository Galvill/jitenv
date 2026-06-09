package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/gv/jitenv/internal/agent"
	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/crypto"
	"github.com/gv/jitenv/internal/gitauth"
	"github.com/gv/jitenv/internal/tui"
)

// newCloneCmd is the user-facing `jitenv clone <url> [dir]` entry
// point for #179. End-to-end: prompt for a PAT, clone via git with
// the token in GIT_ASKPASS, write the token to an encrypted bag,
// and add a cwd_glob mapping so future `git` calls inside the new
// repo see the credential without any of it ever landing in
// .git/config or ~/.netrc.
//
// The flags map 1:1 to the spec in #179:
//
//	--token-stdin     read the PAT from stdin instead of /dev/tty
//	--bag <name>      reuse an existing bag rather than auto-deriving
//	--no-prompt       skip the post-clone "add more mappings?" prompt
func newCloneCmd() *cobra.Command {
	var tokenStdin bool
	var bagOverride string
	var noPrompt bool
	c := &cobra.Command{
		Use:   "clone <https-url> [dir]",
		Short: "Clone a git repo and store its auth token in an encrypted bag.",
		Long: `Clone an HTTPS git repository, prompting once for a personal access token, then store the token in an encrypted jitenv bag and wire a cwd_glob mapping so future git commands inside the cloned directory pick it up via GIT_ASKPASS — never landing the token in .git/config or ~/.netrc.

Only https:// URLs are supported in this release. SSH key support is tracked separately.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClone(cmd, args, tokenStdin, bagOverride, noPrompt)
		},
	}
	c.Flags().BoolVar(&tokenStdin, "token-stdin", false, "read the PAT from stdin instead of the controlling tty")
	c.Flags().StringVar(&bagOverride, "bag", "", "reuse the named bag (must already have a 'token' key) instead of auto-creating one")
	c.Flags().BoolVar(&noPrompt, "no-prompt", false, "skip the post-clone offer to add more mappings")
	return c
}

func runClone(cmd *cobra.Command, args []string, tokenStdin bool, bagOverride string, noPrompt bool) error {
	urlArg := args[0]
	cleanedURL, bagHint, err := gitauth.ParseCloneURL(urlArg)
	if err != nil {
		return err
	}

	destDir := ""
	if len(args) == 2 {
		destDir = args[1]
	} else {
		destDir = defaultDestDir(cleanedURL)
	}
	absDest, err := filepath.Abs(destDir)
	if err != nil {
		return fmt.Errorf("resolve dest dir: %w", err)
	}
	if _, err := os.Stat(absDest); err == nil {
		return fmt.Errorf("%s already exists; refusing to clobber", absDest)
	}

	// Load the encrypted config so we can write the bag + mapping
	// AFTER the clone succeeds. Prompt the passphrase up-front so a
	// failed unlock doesn't cost a clone round-trip; do the actual
	// decrypt below.
	cfgPath, err := config.Resolve(os.Getenv("JITENV_CONFIG"))
	if err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load %s: %w (run `jitenv config init` first)", cfgPath, err)
	}
	pw, err := crypto.PromptPassphrase("jitenv clone passphrase: ", false)
	if err != nil {
		return err
	}
	defer zeroBytes(pw)
	key, err := config.DeriveKeyFromMeta(cfg, pw)
	if err != nil {
		return err
	}
	defer zeroBytes(key)
	// One-shot opaque-ID migration (#248) so a legacy config cloned into
	// gets the sealed name_map + backup like the unlock/TUI paths, rather
	// than silently migrating on save without a backup.
	migrated, err := migrateOpaqueIDsLocked(cfgPath, key)
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

	// Sanity-check bag override BEFORE prompting for the token —
	// nothing worse than typing a PAT then learning the bag name
	// was wrong.
	if bagOverride != "" {
		if cfg.Secrets == nil {
			return fmt.Errorf("--bag %q: no [secrets] block in config; run without --bag to create a fresh bag", bagOverride)
		}
		if _, ok := cfg.Secrets[bagOverride]; !ok {
			return fmt.Errorf("--bag %q: bag is not defined in config", bagOverride)
		}
	}

	// Prompt for the PAT. Mirror PromptPassphrase's silent-tty
	// behaviour so an over-the-shoulder observer doesn't see the
	// token. --token-stdin lets scripts pipe a value in.
	token, err := readToken(cmd.InOrStdin(), tokenStdin)
	if err != nil {
		return err
	}
	defer zeroBytes(token)

	// Ensure the per-user askpass shim exists. Bake the running
	// jitenv's absolute path into it so subsequent git invocations
	// resolve the helper independently of $PATH (gitauth.EnsureShim
	// handles read-compare-write — the second clone for the same
	// jitenv install is a no-op).
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve jitenv executable: %w", err)
	}
	shimPath, err := gitauth.EnsureShim(exe)
	if err != nil {
		return fmt.Errorf("install askpass shim: %w", err)
	}

	// Run git clone. The PAT lives only in the env of this one
	// child process; jitenv's own env never touches it. On clone
	// failure (bad token, repo not found, network error), don't
	// write the bag — the user gets to retry without leftover state.
	if err := runGitClone(cleanedURL, absDest, shimPath, token); err != nil {
		return err
	}

	// Pick the final bag name. If the user passed --bag, reuse it
	// (and DON'T overwrite the existing token — they explicitly
	// opted into sharing). Otherwise derive + dedupe from the URL.
	finalBag := bagOverride
	created := false
	if finalBag == "" {
		taken := bagSet(cfg)
		finalBag = gitauth.DedupeBagName(bagHint, taken)
		created = true
	}

	// Build the bag entry. EncryptField wraps the cleartext under
	// the AAD that binds it to (bag, key) so a transplanted
	// envelope is rejected on decrypt (security #110).
	if created {
		envelope, err := crypto.EncryptField(key, string(token), config.SecretAAD(finalBag, "token"))
		if err != nil {
			return fmt.Errorf("encrypt token: %w", err)
		}
		if cfg.Secrets == nil {
			cfg.Secrets = map[string]map[string]string{}
		}
		cfg.Secrets[finalBag] = map[string]string{"token": envelope}
	}

	// Wire the cwd_glob mapping. The literal-value GIT_ASKPASS
	// VarRef points at the per-user shim; JITENV_GIT_TOKEN pulls
	// from the bag at fetch time. cwd_glob `**` matches the entire
	// repo subtree.
	cfg.Mappings = append(cfg.Mappings, config.Mapping{
		CwdGlob:  filepath.ToSlash(absDest) + "/**",
		Commands: []string{"git"},
		Vars: []config.VarRef{
			{Name: gitauth.JitenvGitTokenEnv, Source: localSourceName(cfg), Ref: finalBag, Key: "token"},
			{Name: "GIT_ASKPASS", Value: shimPath},
		},
	})

	// Ensure a local source exists for the bag. The TUI's "Local"
	// source is the canonical entry; auto-create one named "local"
	// if the config doesn't have any yet.
	if cfg.Sources == nil {
		cfg.Sources = map[string]config.SourceConfig{}
	}
	if _, ok := cfg.Sources[localSourceName(cfg)]; !ok {
		cfg.Sources["local"] = config.SourceConfig{Type: "local"}
	}

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("internal: generated config is invalid: %w", err)
	}
	// Surface intra-mapping env-var collision warnings (#251) on the
	// decrypted, ID→name-translated config BEFORE re-encrypting for
	// save. Advisory only — the clone already succeeded; this never
	// blocks the write.
	emitConfigWarnings(cmd.ErrOrStderr(), cfg)
	if err := saveAndReencrypt(cfgPath, cfg, key); err != nil {
		return err
	}
	pingAgentReloadFromClone()

	fmt.Fprintf(cmd.OutOrStdout(), "\ndone. mapped %s/** → git (token bag: %s)\n", absDest, finalBag)

	// Phase 5 (#179 post-clone follow-up): offer to add more
	// mappings interactively. Skipped under --no-prompt, on
	// non-TTY stdin, or when CI is set.
	if !noPrompt && shouldOfferPostClonePrompt() {
		yes, err := askYesNo(cmd.OutOrStdout(), cmd.InOrStdin(), "Add more mappings or secrets for this repo?")
		if err == nil && yes {
			template := config.Mapping{
				CwdGlob:  filepath.ToSlash(absDest) + "/**",
				Commands: []string{},
				Vars:     []config.VarRef{},
			}
			hint := "cloned just now: " + absDest
			if err := tui.RunWithMappingTemplate(cfgPath, key, &template, hint); err != nil {
				// Don't fail the whole clone — the bag + git mapping
				// are already saved. Just surface the TUI error so
				// the user can re-enter via `jitenv config` if they
				// want.
				fmt.Fprintf(cmd.ErrOrStderr(), "note: post-clone TUI exited with error: %v\n", err)
			}
		}
	}
	return nil
}

func defaultDestDir(cleanedURL string) string {
	last := cleanedURL
	if i := strings.LastIndex(cleanedURL, "/"); i >= 0 {
		last = cleanedURL[i+1:]
	}
	return strings.TrimSuffix(last, ".git")
}

func readToken(stdin io.Reader, fromStdin bool) ([]byte, error) {
	if fromStdin {
		// Read until EOF or newline, strip a trailing newline.
		// bufio.Reader.ReadBytes('\n') returns the delimiter
		// when present; trim so the token doesn't get a \n suffix.
		br := bufio.NewReader(stdin)
		line, err := br.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		line = trimNewline(line)
		if len(line) == 0 {
			return nil, errors.New("--token-stdin: no input on stdin")
		}
		return line, nil
	}
	return crypto.PromptPassphrase("Token (PAT): ", false)
}

func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

// runGitClone exec's `git clone <url> <dest>` with the PAT in the
// child's env (JITENV_GIT_TOKEN) and GIT_ASKPASS pointing at our
// shim. The user sees git's normal progress output on the terminal.
//
// We DON'T use jitenv's syscall.Exec-replace path here — we need to
// continue running after git finishes so we can write the config.
func runGitClone(url, dest, shimPath string, token []byte) error {
	cmd := exec.Command("git", "clone", url, dest)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Child env: parent env minus any pre-existing JITENV_GIT_TOKEN
	// (defence in depth — caller might have a stale one set), plus
	// our two var injections.
	env := stripEnv(os.Environ(), gitauth.JitenvGitTokenEnv)
	env = append(env, gitauth.JitenvGitTokenEnv+"="+string(token))
	env = append(env, "GIT_ASKPASS="+shimPath)
	// GIT_TERMINAL_PROMPT=0 stops git from falling through to its
	// interactive prompt if the PAT is rejected — surface a hard
	// error instead so the user knows to retry with a fresh token.
	env = append(env, "GIT_TERMINAL_PROMPT=0")
	cmd.Env = env

	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("git clone exited %d", ee.ExitCode())
		}
		return fmt.Errorf("git clone: %w", err)
	}
	return nil
}

func stripEnv(env []string, key string) []string {
	prefix := key + "="
	out := env[:0]
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			out = append(out, e)
		}
	}
	return out
}

// bagSet returns the set of bag names already in cfg.Secrets. Used
// by DedupeBagName to avoid collisions when a user clones two repos
// that derive the same bag-name hint.
func bagSet(cfg *config.Config) map[string]struct{} {
	out := map[string]struct{}{}
	for name := range cfg.Secrets {
		out[name] = struct{}{}
	}
	return out
}

// localSourceName picks the local-source name to attach the bag to.
// Defaults to "local"; if the user already has a renamed local
// source (e.g. "vault" because they migrated), use the first
// type=local entry instead.
func localSourceName(cfg *config.Config) string {
	for name, s := range cfg.Sources {
		if s.Type == "local" {
			return name
		}
	}
	return "local"
}

// saveAndReencrypt re-encrypts plaintext-on-Sources/Secrets values
// before AtomicSave, mirroring what the TUI does on every save.
// Plaintext values land in the in-memory Config after DecryptInPlace;
// they have to be re-wrapped or AtomicSave would write the secrets
// to disk in cleartext.
func saveAndReencrypt(path string, cfg *config.Config, key []byte) error {
	if err := config.EncryptInPlace(cfg, key); err != nil {
		return fmt.Errorf("re-encrypt: %w", err)
	}
	return config.AtomicSave(path, cfg)
}

// pingAgentReloadFromClone is a duplicate of internal/tui.pingAgentReload,
// kept package-local so cli doesn't import tui. Errors are
// deliberately ignored — agent might be locked or not running, both
// of which are fine; the user will see the new mapping on next unlock.
func pingAgentReloadFromClone() {
	paths, err := agent.DefaultPaths()
	if err != nil {
		return
	}
	if _, statErr := os.Stat(paths.Socket); statErr != nil {
		return
	}
	cli := agent.NewClient(paths.Socket)
	_ = cli.Reload(context.Background())
}

// shouldOfferPostClonePrompt mirrors the predicates in #179: TTY
// stdin, CI not set. The function is small enough that callers can
// override at the flag level.
func shouldOfferPostClonePrompt() bool {
	if os.Getenv("CI") != "" {
		return false
	}
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// askYesNo prints prompt + " [y/N] " and reads a single character
// response from in. Empty / unknown answers default to N (no).
func askYesNo(out io.Writer, in io.Reader, prompt string) (bool, error) {
	fmt.Fprintf(out, "%s [y/N] ", prompt)
	br := bufio.NewReader(in)
	line, err := br.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes", nil
}
