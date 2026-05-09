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
file* you executed. A cwd_glob mapping is broader: every command in
its required `commands = [...]` list, run inside the matched
directory, gets the env vars. The boundary is "one descendant
process at a time", but the set of triggering commands is anything
you (or anything you run) types in that directory whose name
appears in the explicit list.

What's still protected:

- The parent shell never has the secrets in its own environ. Each
  triggering command gets them at exec, and they live in that
  child's process tree only.
- Only commands you explicitly listed get wrapped. Wildcard
  ("any command") is rejected; the symlink farm is built strictly
  from the `commands` list.
- `cd`-ing out of the mapped directory in the calling shell
  removes the wrapper symlinks on the next prompt — but doesn't
  strip the secrets from already-running children, which inherited
  them at spawn time.

What to be aware of:

- A malicious binary on `$PATH` named the same as a wrapped command
  (e.g. you have `commands = ["npm"]` and there's a rogue `npm`
  earlier in `$PATH` than the system one) would receive the env
  vars too. Same exposure profile as `direnv`'s `PATH_add`.
- The wrapper directory lives under `$XDG_RUNTIME_DIR/jitenv/shells/`
  with mode 0700 — anyone who can write to that path could plant
  a malicious symlink. The runtime dir itself is mode 0700 and
  user-only by systemd-tmpfiles.
- On agent down, the shim runs the real command with the parent
  env (no wrapping). Same UX as the locked-agent path elsewhere.

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
