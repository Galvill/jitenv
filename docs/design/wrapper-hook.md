# Design: split the hook — DEBUG trap for paths, PATH-prepend wrappers for cwds

Status: **Approved direction**. Supersedes the all-DEBUG-trap cwd_glob
implementation in PR #31; the schema and agent-protocol changes from that
PR mostly carry over (one rename — see [Migration](#migration)).

## Problem

The current shell hook uses bash's DEBUG trap (zsh: `preexec`) to do two
unrelated jobs:

1. **Path-mapped scripts** — intercept `./deploy.sh`, `~/scripts/foo`, etc.,
   re-exec through `jitenv run` with merged env.
2. **Cwd-mapped commands** — when running any command inside a configured
   directory, inject env vars.

Job 1 is a good fit for the DEBUG trap: `BASH_COMMAND` is unambiguous for an
explicit path, and the failure mode is bounded (it either is or isn't a path
prefix). Job 2 leaks: the DEBUG trap fires on commands inside completion
functions, `command_not_found_handle`, `PROMPT_COMMAND` helpers like
`__git_ps1`, aliases that expand to absolute paths, and aliases run by
`PROMPT_COMMAND`. Each of those produced a real bug we shipped a fix for.

Distinguishing "user typed this" from "bash internal machinery typed this"
in the DEBUG trap is an arms race we lose.

## Decision

Use the right tool for each job:

- **Path-mapped → keep the DEBUG/preexec trap.** It's reliable for explicit
  paths. Hardening continues (FUNCNAME guards, `COMP_LINE` guard, socket-
  presence pre-check). Add interpreter-form detection
  (`bash <script>`, `python <script>`, …) as a follow-up.

- **Cwd-mapped → switch to PATH-prepend wrappers** (the direnv / asdf /
  mise model). On `chpwd` into a mapped directory, generate a per-shell
  bin dir containing one symlink per *explicitly configured* command,
  pointing at a `jitenv __shim` subcommand. Prepend the dir to `$PATH`;
  remove on `chpwd` out. The shim handles the agent round-trip and
  `syscall.Exec`s the real command with merged env.

This makes Job 2 invisible to the DEBUG trap. PROMPT_COMMAND helpers,
completion compfuncs, command-not-found handlers, etc. invoke shell
functions or unrelated PATH binaries — none of which are wrapped — so they
never enter our code path.

### Decided open questions

| Q | Decision |
|---|---|
| Wrapper scope per cwd_glob | **Explicit `commands = [...]` only** — least-privilege, matches AWS ARN list pattern. Wildcard rejected. |
| Where the shim lives | **`jitenv __shim` subcommand**, dispatched via `os.Args[0]` basename. One binary to ship. |
| Cleanup of stale wrapper dirs | **GC on next agent start**, no per-shell `EXIT` trap. `XDG_RUNTIME_DIR` wipe on logout is the safety net. |
| Multi-shell parity | **Same code path for bash and zsh**; fish/dash become trivially supportable later. |
| Direnv coexistence | Defer. Both prepend to `$PATH`; should compose, but not validated. |

## Architecture

### Config schema

```toml
[[mappings]]
cwd_glob = "~/work/acme"
commands = ["npm", "yarn", "node"]   # required; empty list is invalid
[[mappings.vars]]
source = "local"
ref    = "acme"
```

`Config.Validate` rejects `cwd_glob` with empty `commands`. The previous
PR-#31 schema used a singular `command` (string); the new schema is plural
(list) — see [Migration](#migration).

### Per-shell wrapper directory

Layout:

```
$XDG_RUNTIME_DIR/jitenv/shells/<shell-pid>/bin/
    npm    -> /usr/bin/jitenv     (symlink)
    yarn   -> /usr/bin/jitenv     (symlink)
    node   -> /usr/bin/jitenv     (symlink)
```

One subdir per shell pid. Created by the shell hook on `chpwd` into a
matched cwd_glob. Removed on `chpwd` out. The `<shell-pid>/` segment lets
the agent GC orphans on startup without coordinating with running shells.

### Shell hook (chpwd)

bash has no native `chpwd`. We implement it via `PROMPT_COMMAND` with a
PWD-diff so the only per-prompt work is one `[[ "$PWD" != "$__JITENV_LAST_PWD" ]]`
check; actual hook work runs only on real cd:

```bash
__jitenv_chpwd() {
    if [[ "$PWD" != "${__JITENV_LAST_PWD-}" ]]; then
        jitenv __chpwd "$$" "${__JITENV_LAST_PWD-}" "$PWD"
        __JITENV_LAST_PWD="$PWD"
    fi
}
PROMPT_COMMAND="__jitenv_chpwd${PROMPT_COMMAND:+;$PROMPT_COMMAND}"
```

zsh uses native `chpwd_functions`:

```zsh
__jitenv_chpwd() {
    jitenv __chpwd "$$" "${OLDPWD-}" "$PWD"
}
chpwd_functions+=(__jitenv_chpwd)
```

`jitenv __chpwd <shell-pid> <oldpwd> <newpwd>` is one fork+exec per cd; it
asks the agent which cwd_glob mappings (and thus which `commands`) match
the new PWD, computes the symlink delta, and updates the per-shell bin
dir. PATH update happens shell-side via the hook reading
`jitenv __chpwd`'s stdout (a single line: `PATH=<new>` or empty for "no
change").

### Shim subcommand

`jitenv __shim` runs whenever a wrapper symlink is invoked. The
dispatcher checks `filepath.Base(os.Args[0])` early: anything that isn't
`jitenv` (the canonical binary name) routes to the shim entrypoint
without going through cobra:

```go
func main() {
    if base := filepath.Base(os.Args[0]); base != "jitenv" {
        shim.Main(base, os.Args[1:])
        return
    }
    cli.Execute()
}
```

`shim.Main`:

1. `cwd, _ := os.Getwd()`.
2. Resolve the real command via `$PATH` minus `filepath.Dir(os.Args[0])`
   (so we don't recurse into the same shim symlink).
3. `agent.Client.FetchEnvCwd(ctx, cwd, cmdName)` for env vars; on
   agent-down, fetch a 0-byte env map and proceed (no warning — the
   user's command runs without env, and the same `jitenv status` UX
   covers the "did you forget to unlock?" path).
4. `syscall.Exec(realPath, append([]string{cmdName}, args...), mergedEnv)`.

### Agent-side GC

On `jitenv unlock` (and any other agent start), walk
`$XDG_RUNTIME_DIR/jitenv/shells/`, stat each `<pid>` subdir, and
`os.RemoveAll` any whose pid is no longer alive (`syscall.Kill(pid, 0)`
returns ESRCH). Cheap, idempotent, runs before `Listen`.

`XDG_RUNTIME_DIR` itself is wiped at logout on systemd-logind systems, so
this GC is mostly belt-and-suspenders. WSL and minimal init systems may
leak across sessions otherwise.

### Failure modes

- **Real command not on `$PATH`.** Shim prints `jitenv-shim: <name>: not
  found on $PATH` to stderr and exits 127 — same shape as bash's "command
  not found".
- **Agent unreachable.** Shim runs the real command with the unmodified
  parent env; no warning, no countdown. (Same UX choice we made for the
  hook in #32 — silent on locked agent, `jitenv status` is the
  out-of-band signal.)
- **Symlink dir missing on disk.** Shell hook re-runs `jitenv __chpwd`
  on the next `cd`, which re-creates it.
- **Stale wrapper directory after a crashed shell.** Cleaned by agent
  GC on next start.

## Migration

PR #31 introduced the cwd_glob feature using the DEBUG trap. The reusable
parts:

- `config.Mapping.CwdGlob` and the index lookup (ancestor-walk).
- Agent protocol additions: `Cwd`/`Command` on the request, `IsMappedCwd`
  / `FetchEnvCwd` on the resolver.
- `has-cwd` sentinel file is no longer needed (the bash trap stops doing
  bare-PATH lookups in this design).

The breaking change vs PR #31:

- `Mapping.Command` (string) → `Mapping.Commands` (`[]string`). Empty
  list is invalid; matches the "explicit only" decision.
- The shell hooks no longer call `jitenv is-mapped --cwd` for bare-PATH
  commands. That code path is removed in favour of the wrapper.

Sequencing:

1. Land the schema + agent protocol pieces from PR #31 (rename `command`
   → `commands`).
2. Implement `jitenv __shim` and the wrapper-directory layout.
3. Implement `jitenv __chpwd` and the bash + zsh hook integration.
4. Strip the bare-PATH branch from the bash/zsh hooks.
5. Tests: an integration test that creates two cwd_glob mappings,
   `cd`s between them, runs the configured commands, and asserts env
   vars land. A separate test that asserts non-listed commands are
   *not* wrapped.

## Out of scope

- Wildcard `commands = "*"`. Rejected as a footgun.
- Composing with `direnv`'s `PATH_add`. Likely fine but unverified.
- An interpreter-form path-mapped enhancement (`bash script.sh`,
  `python script.py`). Filed for follow-up after the wrapper redesign
  ships.
- Caching `is-mapped` results in shell state. The wrapper redesign
  removes the per-prompt is-mapped traffic that motivated this idea.

## Why not a separate `jitenv-shim` binary

Considered. Cold-start of a slim binary is ~15-30ms vs ~50-80ms for
`jitenv`'s full cobra graph. Worth it if shim invocations are
performance-critical. Decided no: the user's `commands` list is small
and explicit, the per-invocation overhead is bounded, and shipping one
binary is simpler. Revisit if profiling shows the cobra graph
dominating.
