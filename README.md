# jitenv

Just-in-time environment variable loader. Holds your secrets in an
encrypted config, and injects them into a configured file's process
tree only — never into the parent shell.

## How it works

1. You declare per-file env-var mappings in `~/.config/jitenv/config.toml`,
   encrypted at rest. Editing happens through an interactive **TUI**
   (`jitenv config`) — there is no plaintext-on-disk edit step.
2. `jitenv unlock` prompts your passphrase once per session, derives the
   master key with Argon2id, and starts a per-user agent on a Unix socket
   (ssh-agent style).
3. A shell hook intercepts execution of mapped files and reroutes them
   through `jitenv run`, which asks the agent for the env vars, exec's
   the file with them, and lets the OS clean them up when the child exits.

## Install

```sh
go install github.com/gv/jitenv/cmd/jitenv@latest
# or
git clone https://github.com/gv/jitenv && cd jitenv && make install
```

## Quick start

```sh
# 1. Open the TUI. On first run it offers to create the config and
#    asks for a passphrase. From there: add local secret bags and
#    mappings, all from the keyboard.
#
#    The first time you save inside the TUI, it offers to install
#    the shell hook (one line appended to ~/.bashrc or ~/.zshrc).
#    You can decline and add it manually with `jitenv hook bash|zsh`.
jitenv config

# 2. Unlock the agent (once per session)
jitenv unlock

# 3. Open a new shell so the hook activates, then run a mapped file —
#    the env vars appear only inside it.
./scripts/deploy.sh
echo "$DATABASE_URL"   # empty in the parent shell
```

## TUI

`jitenv config` is the home screen for everything. The whole UI is
**menu-driven** — every place that references a configured object
(a bag name, a key inside a bag, the path/glob kind for a mapping…) is
selected from a list rather than typed, so identifiers stay consistent.

The main menu lists three sections; each opens a list-style page:

```
Mappings      (5 defined)
Local secrets (3 bags)
Settings
```

### List + popup pattern

Every list page is the same shape: a `< Create New … >` sentinel at
the top, then existing items. Selecting the sentinel opens an input
popup; selecting an existing item opens a small bordered popup menu
with the available actions:

```
< Create New Bag >                ← Enter opens a "name new bag" input
stripe       (2 keys)
db           (3 keys)             ← Enter opens:  Edit / Rename / Delete / Back
```

The same shape repeats for the bag detail page (keys inside a bag) and
the mappings page.

### Editing a mapping

Selecting a mapping (or `< Create New Mapping >`) drills into a
3-row editor:

```
kind:      path
target:    /home/me/scripts/deploy.sh
variables: 4 selected
```

Enter on each row opens its own popup:

- **kind** — choose `path` or `glob`.
- **target** — type the path or glob.
- **variables** — opens a bag→key tree with checkboxes.

The variable tree shows every local-secret bag with its keys indented
beneath. Ticking the bag includes the whole bag (now and any future
keys); ticking individual keys produces named env vars per key. While
a bag is in "all" mode, the individual key boxes render dimmed.

### Local secrets

Local-secret values are AEAD-encrypted with the master key and stored
inline in `config.toml` — no extra file, no plaintext on disk.

Renaming a bag, a source, or a key automatically rewrites every
mapping that referenced it, so existing mappings stay valid.

You can also invoke a mapped file explicitly without the hook:

```sh
jitenv run ./scripts/deploy.sh arg1 arg2
```

## Commands

```
jitenv config           Open the interactive TUI (mappings, local secrets, settings)
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

Three first-party sources ship in the binary: **local** (encrypted
bags in `config.toml`), **AWS Secrets Manager**, and **GitHub
Variables/Secrets**. Each is configured from the TUI's *Local secrets*
or *Remote Sources* page; sensitive params are stored as `enc:v1:`
envelopes encrypted under the master key. Adding a new source is two
files — see [docs/source-plugins.md](docs/source-plugins.md).

### Local (`type = "local"`)

Each bag holds multiple `KEY = value` pairs. Values are encrypted with
the master key as `enc:v1:<base64(nonce ‖ ciphertext)>` envelopes, so
the on-disk form looks like:

```toml
[secrets.stripe]
STRIPE_PK = "enc:v1:…"
STRIPE_SK = "enc:v1:…"
```

The TUI auto-creates a `[sources.local]` entry the first time you add
a bag so mappings can immediately reference it:

```toml
[[mappings]]
path = "/home/me/scripts/deploy.sh"
[[mappings.vars]]
name   = "STRIPE_SK"
source = "local"
ref    = "stripe"          # bag name
key    = "STRIPE_SK"       # optional; without it, every key in the bag is exported
```

When `name` and `key` are both empty, every key in the named bag
becomes its own env var (using the bag's keys as env-var names) — the
"include the whole bag" mode you tick in the variable tree.

### AWS Secrets Manager (`type = "aws"`)

Configure via *Remote Sources* in the TUI. All credential fields
(access key ID, secret access key, optional session token, optional
assume-role ARN + external ID, optional endpoint override) are stored
as `enc:v1:` envelopes for sensitive items. Leaving the credential
fields empty falls back to the AWS default credential chain
(env vars, shared config, IRSA). Use the *Test* button on the source
form to ping STS `GetCallerIdentity` for immediate feedback.

### GitHub (`type = "github"`)

Configure via *Remote Sources*. Keep in mind the GitHub API does not
expose secret *values*; the source pulls Variables (readable) and
Secret names (for mapping completeness). Map secret values from a
different source.

## Encryption & threat model

- Master key derived via Argon2id (time=3, mem=64MiB, threads=4) from
  your passphrase + per-config salt.
- Sensitive fields stored as `enc:v1:<base64(nonce ‖ XChaCha20-Poly1305(plaintext))>`
  envelopes.
- Local-secret bag values **always** live in this envelope form on disk.
- The unlocked key lives only in the agent's memory.
- Trust boundary = local user, same as `ssh-agent`. The socket is mode
  0600 and the agent verifies the connecting peer's UID via `SO_PEERCRED`.
- Local root sees plaintext memory. `mlock` helps against casual
  exposure, not against root.

## Agent lifecycle

- The idle timeout in **Settings** is rolling — every request bumps
  `last_seen`, and the agent shuts down (closes its socket and removes
  its pidfile) once the gap exceeds the configured duration. The check
  runs on a 30-second tick, so actual shutdown lags the timeout by up
  to one tick.
- Because the shell hook calls `jitenv is-mapped` on every command, an
  active hooked shell counts as continuous activity and keeps the
  agent alive indefinitely.
- An empty / zero idle timeout disables the auto-shutdown loop.
- Saving in the TUI best-effort pings the running agent (`OpReload`)
  to pick up your changes without `jitenv lock` + `unlock`.

## Limitations

- The bash hook only intercepts commands whose first token is an
  absolute or `./`-relative path — not bare PATH lookups. Intentional,
  keeps the hook fast and predictable.
- Single agent per user; multiple terminals share one unlocked instance.
- Linux-focused (uses `SO_PEERCRED`, `XDG_RUNTIME_DIR`, double-fork via
  `Setsid`).
- The TUI requires a TTY. For scripted setup, use `jitenv config init`
  then re-run interactively.

## Building from source

```sh
make build           # ./bin/jitenv
make test            # go test ./...
make install         # installs to $PREFIX/bin (default ~/.local/bin)
```

Go 1.22+ required.

## License

[MIT](LICENSE).
