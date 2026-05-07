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

The hook calls `jitenv is-mapped <path>` and switches on its exit code:

| Code | Meaning | Hook behaviour |
|---|---|---|
| **0** | mapped | Re-run the command via `jitenv run` so the env vars are injected. |
| **1** | not mapped | Run the command normally — no jitenv involvement. |
| **2** | agent unreachable | (See "agent-absence short-circuit" below.) |

You can see the dispatch in `internal/shell/snippets/bash.sh`. Set
`JITENV_HOOK_DEBUG=1` in your shell to see which branch the trap
took for each command.

### Agent-absence short-circuit

Before any of the dispatch above, the hook stat-checks the agent's
unix socket. If the socket file isn't there (because `jitenv lock`
removed it on shutdown), the trap **returns immediately**. No socket
dial, no `is-mapped` process spawn, no warning.

This matters because the bash DEBUG trap fires on every simple command
bash is about to execute — including the dozens that PROMPT_COMMAND,
prompt-side `$()`s, aliases that expand to absolute paths, and
`~/bin/...` substitutions inject between user keystrokes. Without the
short-circuit, each of those would dial the agent (and on a path
mismatch, paint the red countdown), turning a locked agent into
many-warnings-per-prompt spam.

The trade-off: when the agent is locked, mapped scripts run silently
without their env vars — there's no in-band notification that you
forgot to unlock. Confirm with `jitenv status` if a mapped script's
behaviour looks off.

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
3. `jitenv unlock` — is the agent up? With a locked agent the hook
   short-circuits silently for every command (see "agent-absence
   short-circuit" above). Mapped scripts run without their env
   vars; `jitenv status` will tell you the agent's state.

### "I get a long pause on every command"

Set `JITENV_HOOK_DEBUG=1` and run a command. You should see exactly
one `is-mapped rc=` line per command, with rc=1 (not mapped) for
unrelated commands. If you're seeing more than one log line per
command, your `PROMPT_COMMAND` or PS1 contains commands that the
DEBUG trap fires on — each one pays a sub-millisecond round-trip on
an unlocked agent, which can add up if there are many.

The agent-down warning that older versions printed is gone; if a
locked agent is the cause, the hook stays silent (see the
short-circuit section above).

### "Why does the hook ignore `deploy.sh` when I'm in `./scripts/`?"

Because the first token is a bare name; the hook only matches
absolute or `./`-relative paths. Either run `./deploy.sh` or set up
a glob mapping that covers the resolved path. See
[concepts.md](concepts.md#path-vs-glob).

### "I edited config.toml but the agent shows old values"

Saving via the TUI auto-pings the agent (`OpReload`) so it picks up
changes without a relock. If you edited the file by hand (rare —
the TUI is the supported edit path), `jitenv lock && jitenv unlock`.
