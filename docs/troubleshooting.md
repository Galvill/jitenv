# Troubleshooting

The fixes for the things that go wrong most often. If your symptom
isn't here, check `jitenv hook status` and `jitenv status` first;
those answer 80% of "why isn't it working".

## "Agent unreachable" — red countdown on every command

Your shell hook is installed, but the agent isn't running.
`jitenv unlock`, type your passphrase, done. If unlock itself fails
with "agent did not start within 3s", read the agent log — by
default it's at `${XDG_RUNTIME_DIR}/jitenv/agent.log`, and at
`/tmp/jitenv-<uid>/agent.log` if `XDG_RUNTIME_DIR` is unset.

To temporarily silence the warning while you debug, `JITENV_HOOK_DELAY=0`
in the shell.

## Hook installed, but mapped commands aren't intercepted

Try in this order:

1. `jitenv hook status` — does it say `installed: yes`? If not, run
   `jitenv hook install`.
2. Are you in a **login** shell (e.g. SSH session)? Bash login shells
   don't read `~/.bashrc` unless one of `~/.bash_profile`,
   `~/.bash_login`, or `~/.profile` sources it. The status output
   tells you whether the chain is wired up. Re-running
   `jitenv hook install` adds the guarded source line.
3. Does the command's first token resolve to a real file — an absolute
   path, a `./`-relative path, or a bare name found on `$PATH`? Names
   that don't resolve (builtins, aliases, functions, typos, and scripts
   in the current dir that aren't on `$PATH`) are not intercepted. See
   [shell-hook.md](shell-hook.md#trigger-semantics).
4. Is the file a symlink? Mappings match the resolved canonical path,
   not the symlink. `ls -L` to confirm what jitenv sees.
5. `JITENV_HOOK_DEBUG=1` and re-run. The hook logs each branch it
   takes — this nails down whether the hook is even being invoked
   for the command.

## "I edited config.toml by hand and now the agent shows old values"

The TUI auto-pings the agent (`OpReload`) on save so it picks up
changes without a relock. Hand-edits skip that ping. Either:

- Re-save once through the TUI to trigger the reload, or
- `jitenv lock && jitenv unlock`.

Note that hand-editing the config is fragile — sensitive values must
be wrapped in `enc:v1:` envelopes encrypted under the current master
key, which the TUI does for you on every save.

## Wrong config file is being loaded

`config.Resolve` checks in this order:

1. `$JITENV_CONFIG` if set.
2. `$XDG_CONFIG_HOME/jitenv/config.toml` if `XDG_CONFIG_HOME` is set.
3. `~/.config/jitenv/config.toml` otherwise.

If you run jitenv with `JITENV_CONFIG=/path/to/foo.toml`, it sticks
to that file regardless of XDG. Useful for per-project configs
or for testing — `unset JITENV_CONFIG` to fall back.

## Forgot the passphrase

There's no recovery path. The Argon2id KDF + AEAD envelopes mean a
forgotten passphrase loses every encrypted value in `config.toml`.
Re-run `jitenv config init` to start fresh; encrypted bags from the
old file are unrecoverable.

If you've automated bringup, store the passphrase in your password
manager when you set it — same as you would any other root credential.

## "I get permission denied on the socket"

The socket is mode 0600 and the agent verifies peer UID. You'll only
see permission denied if:

- A different user is trying to talk to the agent. Don't.
- A stale socket from a previous user still exists. Delete the
  contents of `$XDG_RUNTIME_DIR/jitenv/` (or `/tmp/jitenv-<uid>/`)
  and re-run `jitenv unlock`.

## TUI looks broken / characters jumbled

The TUI requires a real TTY and respects `$TERM`. In tmux/screen,
make sure your `$TERM` is `xterm-256color` or `tmux-256color`. SSH
sessions over `mosh` are usually fine. If you're piping `jitenv config`
output anywhere, don't — for scripted setup use `jitenv config init`.

## `jitenv unlock` says hook isn't loaded but I just installed it

`jitenv hook install` only modifies the rc files; it does not
re-source them in your current shell. Open a new shell, or
`source ~/.bashrc` (`source ~/.zshrc`) in the existing one.

## Where to file a bug

Bugs and feature requests go to
<https://github.com/Galvill/jitenv/issues>. Please include `jitenv
status` output and `jitenv hook status` output, plus what shell
(`echo $SHELL`) and what distro (`uname -a`).
