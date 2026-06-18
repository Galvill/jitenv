# Adding a source plugin

How to add a new secret backend (e.g. HashiCorp Vault, GCP Secret
Manager, 1Password) so the rest of jitenv — the resolver, the TUI,
the hook — can use it.

## The contract

Every source implements `pkg/source.Source`:

```go
type Source interface {
    Name() string
    Validate(ctx context.Context) error
    Fetch(ctx context.Context, ref SecretRef) (map[string]string, error)
}
```

- **`Name`** returns the registered type name as it appears in
  `[sources.<...>] type = "<name>"`.
- **`Validate`** confirms reachability/auth without fetching real
  secrets. The TUI and `jitenv sources test` call this — make it
  cheap, idempotent, and deterministic.
- **`Fetch`** returns one or more env-var values keyed by env-var
  name. A single-key `SecretRef` returns one entry; a "expand the
  whole bag" ref returns multiple.

Plus a `Constructor`:

```go
type Constructor func(cfg map[string]any) (Source, error)
```

`cfg` is the contents of `[sources.<name>.params]` after envelope
decryption — params arrive as plaintext strings, not `enc:v2:`
envelopes, because the config decrypt pass walks the params block
before construction.

Optionally implement `Schemed`:

```go
type Schemed interface {
    Schema() []ParamField
}
```

This is what lets the TUI render typed form fields (mask sensitive
inputs, validate required fields, present enums) instead of falling
back to a generic key/value editor. Strongly recommended for any
source whose params need to feel polished in the UI.

## Steps

1. **Create the package** under `internal/sources/<name>/`. Conventional
   shape:

   ```
   internal/sources/myvault/
     myvault.go        # Source impl
     myvault_test.go   # unit tests, including a fake server when feasible
   ```

2. **Register on init.** In your package:

   ```go
   func init() {
       sources.Register("myvault", New)
   }

   func New(cfg map[string]any) (source.Source, error) { ... }
   ```

3. **Wire it into the binary.** Add a blank import in
   `internal/sources/builtin/builtin.go`:

   ```go
   import (
       _ "github.com/gv/jitenv/internal/sources/myvault"
   )
   ```

   `cmd/jitenv/main.go` already blank-imports `internal/sources/builtin`,
   so a single line here makes the source available everywhere.

4. **Implement `Schema()` if you want a polished UI.** Mark sensitive
   fields with `Sensitive: true` so the TUI masks them. Note that
   *every* param is encrypted on disk regardless — `Sensitive` only
   drives UI masking, not whether the value is sealed (see below).

5. **Write tests.** The existing `local` source has the simplest
   shape; `aws` and `vault` show how to mock the backend for unit
   tests.

## Sensitive fields and encryption

**Encrypt-by-default (#112).** On save, the config encrypter seals
*every* non-empty, non-envelope string in a source's `params` map —
whether or not the schema flagged it `Sensitive`. So a source that
ships no `Schema()` at all (a generic key/value source) still gets all
of its param values sealed; you never have to remember to mark a field
to keep it off disk in plaintext. The `Sensitive` bit on a
`ParamField` only controls **UI masking** in the TUI.

Each sealed value is an `enc:v2:` envelope bound to a per-field
associated-data string derived from the source's opaque ID and the
param key (`src.<id>.<param>`), so a ciphertext can't be transplanted
between fields. The agent's decrypt pass walks the whole `params` map
and replaces any envelope string with its plaintext before your
constructor sees it. You don't need per-source crypto code.

## Worked example: the `vault` source

The `vault` source (`internal/sources/vault`) is a good template for a
network-backed backend with cross-field validation:

```toml
[sources.prod_vault]
type = "vault"
[sources.prod_vault.params]
address     = "https://vault.example.com:8200"
auth_method = "approle"
role_id     = "db_role"
secret_id   = "enc:v2:…"   # sealed on disk
mount       = "secret"
kv_version  = "v2"
```

Then reference a KV path from a mapping. `ref` is the path under the
mount; an empty `key` expands the whole secret into one env var per
field (the same "expand the whole bag" shape the `local` source uses):

```toml
[[mappings]]
path = "/home/me/scripts/migrate.sh"
[[mappings.vars]]
source = "prod_vault"
ref    = "myapp/prod"      # reads secret/data/myapp/prod for kv v2
# name + key omitted → every field of the secret becomes its own env var
```

What the `vault` package demonstrates for your own source:

- **A `Schema()` with enums and a sensitive field** — `auth_method`
  is an `Enum: ["token", "approle"]`; `token` and `secret_id` are
  `Sensitive: true`.
- **Cross-field validation the per-field `Required` flag can't
  express** — e.g. "`token` is required when `auth_method=token`".
  `vault` does this in a `validateStatic()` called from its
  constructor.
- **A cheap, side-effect-free `Validate()`** — it authenticates
  (and caches the client) without reading any secret, which is what
  `jitenv sources test` invokes.
- **Defensive `Fetch()`** — it rejects `..` path-traversal refs before
  hitting the network and stringifies non-string values so the
  returned map is always `map[string]string`.

## Special: the `local` source and `bagSink`

`local` is unusual: it has no `params` block, and its data lives
under `[secrets.<bag>]` rather than `[sources.local.params]`. The
agent's resolver injects bag values post-construction via a
`SetBags(...)` type assertion (`bagSink` interface in
`internal/agent/resolver.go`).

You probably don't need this — it exists to keep plaintext bag
values from ever leaving agent memory. If your source also has
"bag-shaped" data structured the same way, see how `local` does it
and follow that pattern.

## External plugins

`pkg/source` is the public package — it carries the canonical
`Source` / `SecretRef` / `Schemed` types so out-of-tree plugins
have a stable surface. Today there's no out-of-process plugin
mechanism in the agent; "plugin" here means "compiled into the
jitenv binary". A plugin protocol over the agent socket is
plausible but not implemented.
