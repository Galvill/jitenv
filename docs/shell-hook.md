# Shell hook

The hook is the thing that makes `./scripts/deploy.sh` magically
have its env vars populated. It's small, intentional, and has a
specific exit-code contract you should know if you ever need to
debug it.

## What's installed

`jitenv hook install` appends one line to your shell's rc file:

```sh
# ~/.bashrc
eval "$(jitenv hook bash)"
# ~/.zshrc
eval "$(jitenv hook zsh)"
```

That one line `eval`s the hook script (`internal/shell/snippets/{bash,zsh}.sh`)
inline at shell startup. The hook doesn't carry secrets — it only
intercepts commands and asks the agent.

For bash, the installer also walks `.bash_profile` → `.bash_login` →
`.profile` and adds a guarded `. ~/.bashrc` line if none of them
already source `~/.bashrc`. Login shells otherwise skip `~/.bashrc`
on most distros, which is the most common "the hook stopped working
when I switched to a login shell" cause. zsh sources `~/.zshrc` for
both interactive and login, so no second file is touched.

`jitenv hook install` is idempotent — re-running it does nothing if
the line is already present.

## Trigger semantics

The hook only intercepts commands whose first token resolves to an
**absolute** or **`./` / `../`-relative** filesystem path. Bare PATH
lookups (`deploy.sh`, `npm`, `python`) are ignored. This is
intentional: it keeps the hook fast (one stat per command, not a
PATH walk) and predictable.

To map a binary you'd otherwise invoke by PATH name, either run it
explicitly through `jitenv run`, or shadow it with a wrapper script
at a known path that's covered by a mapping.

## The exit-code contract

The hook calls `jitenv is-mapped <path>` and switches on its exit
code. **All three branches matter** — collapsing 2 into 1 would be a
bug:

| Code | Meaning | Hook behaviour |
|---|---|---|
| **0** | mapped | Re-run the command via `jitenv run` so the env vars are injected. |
| **1** | not mapped | Run the command normally — no jitenv involvement. |
| **2** | agent unreachable | Print a red 10-second warning. Ctrl+C aborts; otherwise the original command runs (with no env-var injection). This is the "agent is locked" UX path. |

You can see the dispatch in `internal/shell/snippets/bash.sh`. Set
`JITENV_HOOK_DEBUG=1` in your shell to see which branch the trap
took for each command.

## Bash internals: `extdebug` + DEBUG trap

Bash's `extdebug` mode lets a `DEBUG` trap return non-zero to cancel
the original command. The hook uses this to short-circuit the
mapped command and re-execute it through `jitenv run`. The whole
mechanism is one trap and a few `[[ ... ]]` checks — no exec wrappers,
no PATH manipulation.

## zsh internals: `preexec`

zsh has the simpler primitive: `preexec` runs before each command
with the command line as its argument. The zsh hook parses the first
token, runs `is-mapped`, and on a hit replaces the command using
zsh's `BUFFER`/`zle` machinery. Same exit-code contract.

## Common troubleshooting

### "I installed jitenv, opened a shell, and nothing happens"

1. `jitenv hook status` — does it say `installed: yes`?
2. If yes but you're in a login shell (e.g. SSH), check the
   `login chain:` line. If it says "does NOT source ~/.bashrc",
   re-run `jitenv hook install` to add the guarded source line.
3. `jitenv unlock` — is the agent up? An unmapped command with a
   locked agent is fine; a mapped command with a locked agent prints
   the red warning. Either way, an unconfigured-but-installed hook
   never breaks unrelated commands.

### "I get a long pause on every command"

Set `JITENV_HOOK_DEBUG=1` and run a command. You should see exactly
one `is-mapped rc=` line per command, with rc=1 (not mapped) for
unrelated commands. If the rc is 2 (agent unreachable), every
command is paying the 10-second timeout — `jitenv unlock` to fix.

The default 10s wait is configurable: `JITENV_HOOK_DELAY=2` in your
shell shortens it to 2 seconds.

### "Why does the hook ignore `deploy.sh` when I'm in `./scripts/`?"

Because the first token is a bare name; the hook only matches
absolute or `./`-relative paths. Either run `./deploy.sh` or set up
a glob mapping that covers the resolved path. See
[concepts.md](concepts.md#path-vs-glob).

### "I edited config.toml but the agent shows old values"

Saving via the TUI auto-pings the agent (`OpReload`) so it picks up
changes without a relock. If you edited the file by hand (rare —
the TUI is the supported edit path), `jitenv lock && jitenv unlock`.
