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
