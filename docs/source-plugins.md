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
decryption — sensitive params arrive as plaintext strings, not
`enc:v1:` envelopes, because `crypto.DecryptStringsInPlace` walks the
config before construction.

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
   fields with `Sensitive: true` so the TUI masks them and the saver
   wraps them in `enc:v1:` envelopes on disk.

5. **Write tests.** The existing `local` source has the simplest
   shape; `aws` shows how to mock the SDK for unit tests.

## Sensitive fields and encryption

Mark a `ParamField` `Sensitive: true` to:

- Mask its value in the TUI.
- Have the saver re-encrypt it as `enc:v1:` on every save (so the
  on-disk form stays a fresh ciphertext under the master key).

The agent's `crypto.DecryptStringsInPlace` walks the whole `params`
map and replaces any `enc:v1:` string with its plaintext before your
constructor sees it. You don't need per-source crypto code.

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
