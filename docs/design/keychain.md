# Design: OS keychain integration for the master key

Status: **Deferred** (re-evaluate after macOS port lands — see [#13](https://github.com/Galvill/jitenv/issues/13)).
Tracking issue: [#18](https://github.com/Galvill/jitenv/issues/18).

This document records the analysis behind the decision so a future contributor (or a future me) doesn't have to redo it.

## Problem

`jitenv unlock` prompts for the master passphrase, derives the key with Argon2id, and hands it to the agent over fd 3. On a dev machine that reboots frequently this re-prompt is friction. Tools like `gh auth`, `pass`, and `op` solve a similar problem by storing a token in the OS keychain (Linux Secret Service, macOS Keychain, Windows Credential Manager).

The question: should jitenv do the same — and if so, store the *passphrase* or the *derived key*?

## Threat model — what changes

The current model:

- Master key lives only in the agent process and the parent process briefly during `unlock`. Both `defer zeroBytes(...)` it.
- An attacker running as the user can already attach a debugger to the agent (`ptrace`) on Linux without `kernel.yama.ptrace_scope=2`, but **must time the attack to a window when the agent is unlocked**. After `jitenv lock`, the key is gone — they can grab encrypted blobs from `config.toml` but need the passphrase to decrypt anything.
- Reboot wipes everything. Cold-boot recovery requires the passphrase.

A keychain-stored key (or passphrase) changes this:

- Cold-boot recovery no longer requires the user. Anyone with code execution as the user, at any time, can ask the keychain. The "must time the attack to an unlock window" guarantee is gone.
- On Linux, Secret Service / GNOME Keyring / KWallet typically auto-unlock at login (PAM module on most desktop distros). So in practice the keychain is unlocked whenever the user is logged in. A malicious binary the user runs has effectively the same access to the keychain as it has to the agent — except it can also persist across `jitenv lock` and across reboots.
- On macOS, Keychain prompts the user the first time a new binary asks for an item, then remembers consent. This is meaningfully better than Linux but still doesn't survive supply-chain compromise of jitenv itself.
- On Windows, DPAPI-backed Credential Manager binds to the user account; still accessible by any process running as that user.

**Net:** keychain integration trades a small UX win (skip passphrase prompt) for a measurable downgrade against the "attacker has user-level code execution" threat. That is a reasonable trade for some users, never for others. **Therefore the feature must be opt-in and off by default.**

## Key vs. passphrase — what to store

Two options:

- **Store the derived key** (32 bytes). Skips Argon2id on unlock — `unlock` becomes instant. Compromise of the keychain hands the attacker a working key.
- **Store the passphrase**. Preserves the KDF as a barrier; attacker still has to do ~64 MiB × 3 iterations of Argon2id per guess, but in practice that's a few hundred ms — meaningless if the keychain entry already contains the *correct* passphrase.

Conclusion: the KDF protects against passphrase guessing, not against possession of the correct passphrase. Once an attacker has the entry, both are equivalent. The KDF buys nothing here. **Store the derived key.** It also dodges a footgun: if we ever change Argon parameters, a stored passphrase needs re-derivation; a stored key is bound to the salt+params already in `config.toml` and works as long as those don't change.

(Salt and Argon params stay in `config.toml` either way, so changing them invalidates the stored key, which is correct behaviour.)

## Library

[`99designs/keyring`](https://github.com/99designs/keyring) is the obvious pick — it's MIT-licensed, mature, used by `aws-vault` and `step-cli`, and wraps Linux Secret Service / KWallet / pass / macOS Keychain / Windows Credential Manager / a libsecret-free file fallback. It also exposes a `KeychainTrustApplication` flag on macOS so we can tag jitenv as authorized after first prompt.

Alternative — write a thin `Keyring` interface with platform impls. Reasonable if we only target one platform, but for a multi-platform v1 the surface area is too large to justify rolling our own.

## UX

- **Default behaviour unchanged.** Users who want passphrase-on-every-unlock keep that.
- New flag `jitenv unlock --remember` stores the derived key in the keychain after a successful unlock. Symmetric `jitenv unlock --forget` removes the entry. (We also need `jitenv keychain forget` for the case where the user lost their config.)
- A new `jitenv unlock` (no flags) **prefers the keychain entry if one exists**, falls back to passphrase prompt if not (or on any keychain error — see fallback below).
- Surface state in `jitenv status`: `master key remembered: yes (keychain)` / `no`.
- `jitenv lock` only wipes in-memory state — does **not** clear the keychain entry. Two distinct verbs, two distinct semantics. Re-locking and re-unlocking should be cheap.

## Fallback

The keychain is unavailable in three real situations:

- Headless / SSH session with no D-Bus and no `gnome-keyring-daemon`.
- CI runners.
- WSL — the user's primary dev platform per repo conventions. WSL has no native Secret Service; you'd need to launch `gnome-keyring-daemon --components=secrets` manually, deal with PAM, and accept that the keychain unlocks on every WSL session start without a password.

Behaviour in these cases: jitenv must transparently fall back to passphrase prompt on any keychain error. Never fail closed. The error path should hint at how to disable keychain attempts entirely (`jitenv unlock --forget` to remove the entry, or a config flag — see below).

## Configuration knob

Add `[keychain]` block to `config.toml`:

```toml
[keychain]
enabled = false  # opt-in; default off
```

- `enabled = false` — never attempt keychain reads or writes, even with `--remember`.
- `enabled = true` — `--remember` works; bare `unlock` reads from keychain if entry exists.

This gives shared/managed-config users a way to lock the feature off centrally.

## Platform support matrix

| Platform | Status for v1 | Notes |
|---|---|---|
| Linux desktop (GNOME/KDE) | Supported | Via Secret Service. PAM-unlocked at login on most distros. |
| Linux server / SSH | Fallback only | No D-Bus, no agent. Fall back to passphrase. |
| WSL | **Out of scope for v1** | Document explicitly. Users can passphrase-cache via the agent's idle timeout instead. |
| macOS | Supported | Best UX of the three; first-use prompt then trusted. **Blocked on [#13](https://github.com/Galvill/jitenv/issues/13).** |
| Windows | Out of scope | No Windows port planned. |

## Why defer instead of build now

1. **macOS port hasn't landed.** Two of the three target platforms are macOS or maybe-someday-Windows; the platform with the *best* keychain story is the one we don't yet support. Building keychain integration now means shipping a Linux-only feature that only meaningfully helps users on the platform with the *worst* keychain story (Linux desktop) or the platform we explicitly don't support (WSL).
2. **Lower-cost UX wins are still on the table.** The agent already has an idle timeout; raising the default (or making it configurable per-mapping) covers most of the "I don't want to retype my passphrase every five minutes" pain at zero security cost. Worth shipping that first and seeing how much keychain demand remains.
3. **Threat-model regression is real.** A user who installs jitenv expecting the published security model and then opts into `--remember` because "it's there" may not realize they've materially changed their exposure. Better to ship this once we have macOS in hand and can write a single, accurate threat-model section that covers all three platforms.

## Decision

**Defer.** Revisit after [#13](https://github.com/Galvill/jitenv/issues/13) (macOS port) is merged. At that point reopen this issue, refresh the threat-model section, and ship the design above behind `[keychain] enabled` as opt-in.

In the meantime, if user feedback consistently asks for "don't retype every reboot," the cheaper interim move is tuning the agent idle timeout and surfacing it in `jitenv status` — not a keychain.
