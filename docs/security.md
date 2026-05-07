# Security model

What jitenv protects against, what it doesn't, and how the cryptography
fits together.

## Threat model in one paragraph

The trust boundary is the local user, same as `ssh-agent`. jitenv
keeps your secrets out of the parent shell environment, off your
filesystem in plaintext, and out of any process tree except the
specific exec'd child you intended to run. It does **not** protect
against an attacker who already has root on the same machine, who
has compromised the running jitenv binary, or who is sniffing the
exec'd child's `/proc/<pid>/environ`.

## Cryptography

- **KDF.** Argon2id with `time=3, mem=64 MiB, threads=4` (see
  `internal/crypto/kdf.go`). Per-config 16-byte random salt stored in
  `config.toml` `_meta.salt`. Re-deriving the key from the same
  passphrase + salt always produces the same 32-byte key.
- **AEAD.** XChaCha20-Poly1305 from `golang.org/x/crypto/chacha20poly1305`.
  Sensitive values are stored as `enc:v1:<base64(nonce ‖ ciphertext)>`
  envelopes in `config.toml`. The 24-byte nonce is freshly randomized
  per encrypt — never reused across saves.
- **Passphrase verification.** `_meta.verify` is a fixed sentinel
  encrypted under the master key during `jitenv config init`. Each
  `jitenv unlock` re-derives the key and decrypts it; failure means
  the wrong passphrase.

## Key handling

The master key is a 32-byte slice. It moves through three places:

1. **Parent process during `unlock`.** Read from passphrase prompt,
   piped into Argon2id, kept just long enough to spawn the agent and
   verify the sentinel. `defer zeroBytes(...)` everywhere it lives.
2. **Inherited fd 3 to the child agent.** `SpawnDaemon`
   (`internal/agent/daemonize.go`) creates a pipe, attaches the read
   end as fd 3 in the child via `cmd.ExtraFiles`, writes the key
   bytes, and closes both ends. The child reads exactly `KeyLen`
   bytes from fd 3 then closes the pipe.
3. **Agent process memory.** Where the key actually lives until
   `jitenv lock` or the idle timeout fires. The agent zeroes the
   key on shutdown.

The key never appears in command-line arguments, environment
variables, or on disk. New code that touches it must follow the
same `defer zeroBytes(...)` pattern.

## Socket access

The agent listens on `$XDG_RUNTIME_DIR/jitenv/agent.sock`, mode
**0600**, owned by the user. The agent additionally verifies the
connecting peer's UID via `SO_PEERCRED` and rejects mismatches.
Both belt and suspenders: kernel-enforced filesystem permission and
explicit peer-credential check.

If `$XDG_RUNTIME_DIR` is not set (some minimal/init-less setups),
the fallback is `/tmp/jitenv-<uid>/`, also mode 0700.

## What the on-disk file looks like

`config.toml` (mode 0600) contains:

```toml
version = 1

[_meta]
kdf = "argon2id"
argon_time       = 3
argon_memory_kib = 65536
argon_threads    = 4
salt   = "base64-of-16-bytes"
verify = "enc:v1:base64-of-nonce-and-ciphertext"

[sources.local]
type = "local"

[secrets.stripe]
STRIPE_PK = "enc:v1:…"
STRIPE_SK = "enc:v1:…"

[[mappings]]
path = "/home/me/scripts/deploy.sh"
[[mappings.vars]]
name   = "STRIPE_SK"
source = "local"
ref    = "stripe"
key    = "STRIPE_SK"
```

Atomic save via `config.AtomicSave` (sibling tempfile + rename, mode
0600) so the file is never half-written.

## What's NOT protected

- **Local root.** Can `ptrace` the agent, read `/proc/<pid>/mem`, and
  pull the master key live. There is no defence at the user-mode
  level; you trust your kernel and your sudoers list.
- **Compromised jitenv binary.** A rogue build of jitenv could log
  decrypted values, exfiltrate the key, or change agent behaviour.
  Releases are signed with cosign keyless; verify before installing
  (see `docs/RELEASING.md`).
- **The exec'd child's `environ`.** While the child runs, its
  `/proc/<pid>/environ` is readable by any process running as you.
  This is exactly the same exposure as setting an env var manually
  — the win is that the parent shell never had the var, so other
  commands you run aren't exposed.
- **Off-host attackers with code execution as your user.** An
  attacker with shell access talks to the agent through the same
  socket you do. `SO_PEERCRED` verifies they're "you" but doesn't
  distinguish *which* of your processes; any binary you run, run
  unwittingly, or have malware in your `$PATH` can ask the agent for
  any mapping.
- **Memory dumps.** No `mlock`. Pages may be swapped. We accept this
  trade — `mlock` requires elevated permissions or per-syscall
  fiddling and offers little against the local-root threat that
  actually matters.

## cwd_glob mappings widen the blast radius

Path and glob mappings only inject env vars into the *one specific
file* you executed — same one-process boundary as `jitenv run`. A
cwd_glob mapping is broader: **every command run inside the matched
directory** that matches the optional `command` filter gets the env
vars. The boundary is now "one descendant process at a time", but
the set of triggering commands is anything you (or anything you run)
types in that directory.

What's still protected:

- The parent shell never has the secrets in its own environ. Each
  triggering command gets them at exec, and they live in that
  child's process tree only.
- `cd`-ing out of the mapped directory in the calling shell does
  **not** strip the secrets from already-running children — they
  inherited them at spawn time, exactly like manual exports work.
- The hook's `command` filter (when set) restricts firing to that
  bare name. `command = "npm"` will not fire for `git`, `ls`, or
  anything else even inside the matched cwd.

What to be aware of:

- Inside a matched cwd, **any** command (or the optional
  `command`-scoped one) gets the secrets. If a malicious binary on
  your `$PATH` is invocable from there, it gets injected too. Treat
  cwd_glob like a per-directory `.envrc` — same trust model as
  `direnv` for the broadening, but secrets still don't leak into the
  parent shell.
- The agent maintains a tiny sentinel file
  (`$XDG_RUNTIME_DIR/jitenv/has-cwd`) so the hook can skip the
  bare-PATH branch entirely when no cwd_glob mapping exists. With at
  least one cwd_glob configured, the hook does a `stat` per command
  + a Unix-socket round-trip on miss; sub-millisecond, but not zero.

If you don't want this widening, simply don't define any `cwd_glob`
mapping — the hook reverts to its zero-overhead exec-only behaviour.

## Why this design over `.env` files

A `.env` file in your project (or `direnv`-style auto-export) puts
the variables in **every** process you run inside that directory —
your shell, your editor, every test, every wayward `curl`. That's
fine for low-stakes development, terrible for credentials with
real blast radius. jitenv narrows the exposure to the one process
that actually needs the value.

The cost: the parent-shell experience is intentionally less
convenient. You can't `echo $STRIPE_SK` to confirm it's set.
That's a feature.
