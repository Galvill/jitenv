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

The hook intercepts a command when its first token resolves to a real
file, whether that token is:

- an **absolute** path (`/usr/local/bin/terraform`),
- a **`./` / `../`-relative** path (`./scripts/deploy.sh`), or
- a **bare name resolved through `$PATH`** (`terraform`, `deploy.sh`) —
  the hook does a single `type -P` / `whence -p` lookup and matches the
  mapping against the resolved absolute path.

Bare names that don't resolve to a real executable file — shell
builtins, aliases, functions, and typos — are ignored, so the common
case (no mapping) stays cheap: one `type -P` and at most one
`jitenv is-mapped` per command, never a manual PATH walk.

So a `path = "/usr/local/bin/terraform"` (or a matching `glob`) mapping
now fires whether you type the full path or just `terraform`.

## The exit-code contract

The hook calls `jitenv is-mapped <path>` and switches on its exit
code. **All three branches matter** — collapsing 2 into 1 would be a
bug:

| Code | Meaning | Hook behaviour |
|---|---|---|
| **0** | mapped | Re-run the command via `jitenv run` so the env vars are injected. |
| **1** | not mapped | Run the command normally — no jitenv involvement. |
| **2** | config unreadable | Run the command normally — no env-var injection, no warning. A broken or missing config is treated like an unmapped path; the user sees nothing unless `JITENV_HOOK_DEBUG=1`. |

`jitenv is-mapped` reads the config file directly and never contacts
the agent, so an exit code 2 always means the on-disk config is
missing or malformed. The agent-unreachable UX (red countdown, **Press
Enter to skip, Ctrl+C to abort**) lives inside `jitenv run` and the
cwd_glob shim — see `internal/agentwarn/agentwarn.go`. It only fires
*after* `is-mapped` returned 0 and `jitenv run` then failed to reach
the agent.

You can see the dispatch in `internal/shell/snippets/bash.sh`. Set
`JITENV_HOOK_DEBUG=1` in your shell to see which branch the trap
took for each command.

## Non-interactive use (CI, scripts)

Two env-var knobs make the hook quiet in scripted contexts:

- `JITENV_NO_NOTICE=1` suppresses the green `jitenv: injected N
  variables` line that `jitenv run` and the shim print on success.
  The conventional `CI=true` (set by GitHub Actions, GitLab CI,
  CircleCI, Travis) has the same effect automatically.
- `JITENV_HOOK_DELAY=0` skips the agent-down countdown. The countdown
  is also auto-skipped when stdin is not a TTY, so piped or
  redirected invocations never block — the warning line still prints
  once so the failure mode is visible in logs.

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

A bare `deploy.sh` only triggers the hook if it resolves through
`$PATH` to a real file. A script in the current directory is *not* on
`$PATH` (jitenv never adds `.` to PATH), so `deploy.sh` doesn't resolve
and is ignored — run `./deploy.sh`, or set up a glob mapping that
covers the resolved path. A binary that *is* on `$PATH` (e.g.
`terraform`) is matched by its resolved absolute path. See
[concepts.md](concepts.md#path-vs-glob).

### "I edited config.toml but the agent shows old values"

Saving via the TUI auto-pings the agent (`OpReload`) so it picks up
changes without a relock. If you edited the file by hand (rare —
the TUI is the supported edit path), `jitenv lock && jitenv unlock`.
