# jitenv

Just-in-time environment variable loader. Fetches secrets from pluggable sources (AWS Secrets Manager, GitHub Variables, encrypted local bags) and injects them only into the process tree of a configured file — never into the parent shell.

## How it works

1. You declare per-file env-var mappings in `~/.config/jitenv/config.toml` (encrypted at rest). Editing happens through an interactive **TUI** (`jitenv config`) — there is no plaintext-on-disk edit step.
2. `jitenv unlock` prompts your passphrase once per session, derives the master key with Argon2id, and starts a per-user agent on a Unix socket (ssh-agent style).
3. A shell hook intercepts execution of mapped files and reroutes them through `jitenv run`, which asks the agent for the env vars, exec's the file with them, and lets the OS clean them up when the child exits.

## Install

```sh
go install github.com/gv/jitenv/cmd/jitenv@latest
# or
git clone https://github.com/gv/jitenv && cd jitenv && make install
```

## Quick start

```sh
# 1. Open the TUI. On first run it will prompt you to create the config
#    and pick a passphrase. From there: add sources, mappings, and any
#    locally-stored secrets — all from the keyboard.
#
#    The first time you save inside the TUI, it offers to install the
#    shell hook (one line appended to ~/.bashrc or ~/.zshrc). You can
#    decline and add it manually with `jitenv hook bash|zsh`.
jitenv config

# 2. Unlock the agent (once per session)
jitenv unlock

# 3. Open a new shell so the hook activates, then run a mapped file —
#    the env vars appear only inside it
./scripts/deploy.sh
echo "$DATABASE_URL"   # empty in parent shell
```

## TUI

`jitenv config` is the home screen for everything:

```
┌─ jitenv ─────────────────────────────┐
│ > Sources       (2)                  │
│   Mappings      (5)                  │
│   Local secrets (3)                  │
│   Settings                           │
└──────────────────────────────────────┘
[a] add  [enter] edit  [d] delete  [esc] back
```

The UI is **menu-driven**: anywhere a configured object is referenced (a source name in a mapping, a bag name when wiring a local secret, a specific key inside a bag, the GitHub scope, the path/glob kind) you pick from a list — never type the identifier. Adding a mapping is a small wizard:

```
+ Add new mapping
   ↳ kind:    [pick path / glob]
   ↳ target:  [type the path or glob once]
   ↳ + Add variable
        ↳ pick source       (configured sources, listed)
        ↳ pick bag          (for local sources)
        ↳ pick mode         (inject all keys / pick one key)
        ↳ pick key          (when picking one)
        ↳ env var name      (defaulted, edit if needed)
   ↳ Save mapping
```

Sensitive fields are masked as you type (`••••`); press `ctrl+r` to reveal. Save inside any form with `ctrl+s` (or by selecting the "Save" row). The unsaved-changes badge in the header reminds you work is pending; quitting with unsaved work prompts to save first. Saving auto-pings a running agent so changes take effect without `jitenv lock` + `unlock`.

You can also invoke explicitly without the hook:

```sh
jitenv run ./scripts/deploy.sh arg1 arg2
```

## Commands

```
jitenv config           Open the interactive TUI (sources, mappings, local secrets, settings)
jitenv config init      Non-interactive: create a fresh encrypted config
jitenv config show      Print the decrypted config to stdout
jitenv config validate  Parse + structural check
jitenv unlock           Prompt passphrase, start agent
jitenv lock             Stop the agent
jitenv status           Agent status
jitenv run <file>       Fetch env, exec file
jitenv is-mapped <file> Exit 0 if file is mapped (used by shell hook)
jitenv sources list     Sources defined in your config
jitenv sources types    Source types compiled in
jitenv sources test <n> Run Validate() against a configured source
jitenv hook bash|zsh    Print shell hook for eval
```

## Sources

### AWS Secrets Manager (`type = "aws"`)

Params: `region`, `profile`, `role_arn`. Auth uses the standard AWS chain. With `key`, the secret must parse as a JSON object and the named key is returned. Without `key`, a JSON object is expanded to all keys, or a raw scalar is returned under `value`.

### GitHub (`type = "github"`)

Params: `token` (PAT), `api_url` (optional, for GHE).

Reads **Variables**, not Secrets. Per-mapping `extra.scope`:

| scope | meaning | `ref` format |
|-------|---------|--------------|
| `repo` (default) | Repo Variables | `owner/repo` |
| `org` | Org Variables | `org` |
| `env` | Environment Variables (also set `extra.environment`) | `owner/repo` |

GitHub does **not** expose Actions/Codespaces/Dependabot Secret values via API. Only Variables are readable; the source returns a clear error if a Secret is referenced.

### Local (`type = "local"`)

Encrypted-at-rest secret bags stored in the same `config.toml` as everything else. Each bag holds multiple `KEY = value` pairs; values are AEAD-encrypted with the master key, never written in plaintext.

Edit bags from `jitenv config → Local secrets`. The TUI auto-creates a `[sources.local]` block the first time you add a bag, so mappings can immediately reference it:

```toml
[[mappings]]
path = "/home/me/scripts/deploy.sh"
[[mappings.vars]]
name = "STRIPE_SK"
source = "local"
ref = "stripe"          # bag name
key = "STRIPE_SK"       # optional; without it, every key in the bag is exported
```

When `name` and `key` are both empty, every key in the named bag becomes its own env var (using the bag's keys as env names). This is the natural way to inject a related set of credentials with a single mapping.

## Encryption & threat model

- Master key derived via Argon2id (time=3, mem=64MiB, threads=4) from your passphrase + per-config salt.
- Sensitive fields stored as `enc:v1:<base64(nonce ‖ XChaCha20-Poly1305(plaintext))>` envelopes.
- Local-secret bag values **always** live in this envelope form on disk.
- Unlocked key lives only in the agent's memory.
- Trust boundary = local user, same as `ssh-agent`. The socket is mode 0600 and the agent verifies the connecting peer's UID via `SO_PEERCRED`.
- Local root sees plaintext memory. mlock helps casual exposure, not root.

## Limitations

- The bash hook only intercepts commands whose first token is an absolute or `./`-relative path — not bare PATH lookups. Intentional, keeps the hook fast and predictable.
- Single agent per user; multiple terminals share one unlocked instance.
- Linux-focused (uses `SO_PEERCRED`, `XDG_RUNTIME_DIR`, double-fork via `Setsid`).
- The TUI requires a TTY. For scripted setup, use `jitenv config init` then re-run interactively.

## Building from source

```sh
make build           # ./bin/jitenv
make test            # go test ./...
make install         # installs to $PREFIX/bin (default ~/.local/bin)
```

Go 1.22+ required.
