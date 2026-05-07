# Concepts

The model jitenv is built on. Read this once and the rest of the
docs make sense.

## Sources, bags, mappings, refs

Four nouns:

- **Source** — a backend that yields secret material. The first-party
  source is `local` (encrypted bags inside `config.toml`); `aws` (AWS
  Secrets Manager) and `github` (Actions secrets/variables) are
  compiled in. New sources plug into `pkg/source` — see
  [source-plugins.md](source-plugins.md).
- **Bag** — for the `local` source only, a named group of `KEY = value`
  pairs. Bags are how you organize related secrets ("stripe", "db",
  "ci"). They live under `[secrets.<bagname>]` in `config.toml` and
  every value is encrypted at rest.
- **Mapping** — a rule that says "when the user runs *this* file, set
  *these* env vars from *those* sources". A mapping has either a
  `path` (exact filesystem path) or a `glob` (e.g. `~/work/**/*.sh`)
  plus a list of `vars`.
- **VarRef** — one entry inside a mapping's `vars` array, naming an
  env var to inject and where to fetch it from.

```toml
# Sources are declared once.
[sources.local]
type = "local"

[sources.prod_aws]
type = "aws"
[sources.prod_aws.params]
region          = "us-east-1"
access_key_id   = "AKIA…"
secret_access_key = "enc:v1:…"   # encrypted at rest

# Bags hold local secret values.
[secrets.stripe]
STRIPE_PK = "enc:v1:…"
STRIPE_SK = "enc:v1:…"

# Mappings tie a file (or glob) to env vars.
[[mappings]]
path = "/home/me/scripts/deploy.sh"
[[mappings.vars]]
name   = "STRIPE_SK"
source = "local"
ref    = "stripe"
key    = "STRIPE_SK"
```

## VarRef shapes

A `VarRef` has four fields. The first three plus an optional `extra`
map cover every shape:

| Shape | What it does |
|---|---|
| `name` + `source` + `ref` + `key` | One env var. Take field `key` from bag/object `ref` in `source`, expose it as `$name`. |
| `name` empty + `source` + `ref` + `key` empty | **Expand the whole bag.** Every key in `ref` becomes its own env var named after the key. |
| `name` empty + `key` non-empty | **Invalid.** Rejected by `Config.Validate()`. |

The "expand" shape is the one most people miss reading the schema.
It's exactly what the TUI's "tick the bag" mode produces — useful when
a bag's keys already match the env var names you want.

## Mapping kinds: path, glob, cwd_glob

A mapping picks **one** target shape:

- `path` — exact filesystem path. Fast lookup.
- `glob` — [doublestar](https://github.com/bmatcuk/doublestar/v4)
  pattern matched against the executed file's resolved path. Supports
  `**`, `*`, `?`, `[…]` and curly alternation. Common patterns:
  `~/work/**/*.sh`, `**/scripts/deploy*`.
- `cwd_glob` — pattern matched against `$PWD` (and any ancestor). For
  cwd mappings the shell hook intercepts **bare-PATH commands**
  (`npm`, `python`, `bundle`, …) run from inside the matched directory
  — useful for "anything I run inside this project gets the project's
  env vars". An optional `command = "<bare-name>"` field scopes the
  mapping to a single command; empty means every command run inside
  the matched cwd.

Lookup order is **declaration order** within each kind: exact paths
first, then each matching glob, then each matching cwd_glob. When two
entries provide the same env var name, the later one wins — so a
glob can supply defaults while a more specific path overrides
individual vars.

```toml
# Anything run inside ~/work/acme gets ACME_API_TOKEN.
[[mappings]]
cwd_glob = "~/work/acme"
[[mappings.vars]]
name   = "ACME_API_TOKEN"
source = "local"
ref    = "acme"
key    = "API_TOKEN"

# Only npm in that directory needs the registry token.
[[mappings]]
cwd_glob = "~/work/acme"
command  = "npm"
[[mappings.vars]]
name   = "NPM_TOKEN"
source = "local"
ref    = "acme"
key    = "NPM_TOKEN"
```

### Performance: the has-cwd sentinel

The bash and zsh hooks short-circuit on bare-PATH commands when no
cwd_glob mapping is configured, so users who never use cwd mappings
pay zero overhead. The agent maintains a tiny sentinel file at
`$XDG_RUNTIME_DIR/jitenv/has-cwd` (or `$TMPDIR/jitenv-<uid>/has-cwd`)
when at least one cwd_glob mapping exists; the hook checks for the
file's existence (one stat) before spending a socket round-trip. The
sentinel is created on agent start and refreshed on every reload.

## Agent

A long-lived per-user process spawned by `jitenv unlock`:

- Holds the master key only in memory.
- Listens on `$XDG_RUNTIME_DIR/jitenv/agent.sock`, mode 0600.
- Verifies the connecting peer's UID via `SO_PEERCRED`.
- Speaks JSON over a length-prefixed framing — see
  `internal/agent/protocol.go`. Ops: `status`, `is_mapped`,
  `fetch_env`, `lock`, `reload`.
- Auto-shuts down after the configured idle timeout. Each request
  bumps `last_seen`; an active hooked shell continuously calls
  `is-mapped`, so it stays alive while you're using it.

`jitenv lock` stops the agent and wipes its in-memory key. The next
`jitenv unlock` re-prompts the passphrase.

## Where the secrets actually live

```
config.toml on disk:        encrypted blobs.
agent process memory:       master key + decrypted bag values.
                            secrets ONLY enter your env via `jitenv run`.
$ ./mapped-script.sh        the hook re-runs through `jitenv run`,
                            which exec's the script with merged env.
parent shell:               never sees the secrets.
```

That third arrow is the whole point of the project: secrets live in
the exec'd child's process tree and nowhere else.
