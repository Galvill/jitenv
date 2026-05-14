# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build / test / lint

```sh
make build        # ./bin/jitenv (trimpath, -s -w)
make install      # to $PREFIX/bin (default ~/.local/bin)
make test         # go test ./...
make fmt vet tidy

# Run one package's tests
go test ./internal/agent

# Run a single test
go test ./internal/run -run TestRunInjectsEnvAndExecs -v
```

Go 1.22+ (go.mod declares 1.25.6). Linux, macOS, and Windows. Peer-cred check is platform-split:
- Linux: `SO_PEERCRED` (`peer_linux.go`), runtime dir `$XDG_RUNTIME_DIR/jitenv/`, config dir `$XDG_CONFIG_HOME/jitenv/` (or `~/.config/jitenv/`).
- macOS: `LOCAL_PEERCRED` (`peer_darwin.go`), runtime dir `os.TempDir()/jitenv-<uid>/` (typically `/var/folders/.../T/jitenv-<uid>`), config dir XDG-style.
- Windows: token-SID peer check (`peer_windows.go`), pipe at `\\.\pipe\jitenv-<sid>` (`socket_windows.go`), runtime dir `%LOCALAPPDATA%\jitenv\`, config dir `%LOCALAPPDATA%\jitenv\` (see `internal/config/path_windows.go`; legacy `%APPDATA%\jitenv\config.toml` is read as a fallback for backward compat).

The `Setsid` double-fork is the Unix daemon-spawn primitive; on Windows the equivalent uses `CREATE_NO_WINDOW | DETACHED_PROCESS` plus an anonymous-pipe master-key handoff (`daemonize_windows.go`). The `jitenv run` and shim exec paths use `syscall.Exec` on Unix and spawn-and-wait + `os.Exit(child)` on Windows (no exec-replace primitive there). PowerShell 7+ is the supported Windows shell — see `internal/shell/snippets/powershell.ps1` and `cwd_glob` wrapper `.ps1` files for the activation model.

The e2e tests under `internal/run` and `internal/shell` shell out to `go build` to produce a real binary against a temp config; they're slow but exercise the unlock → daemonize → hook → run loop end-to-end.

## Big-picture architecture

jitenv is a 3-process design that keeps secrets out of the parent shell:

1. **CLI / TUI** (`cmd/jitenv` → `internal/cli`, `internal/tui`) — reads/writes the encrypted TOML config. The TUI decrypts in-process, edits, and re-encrypts on save.
2. **Agent** (`internal/agent`) — long-lived per-user daemon, spawned by `jitenv unlock` via `SpawnDaemon` (re-execs `jitenv __agent` with the master key handed over fd 3). Listens on a Unix socket under `$XDG_RUNTIME_DIR/jitenv/` (Linux) or `$TMPDIR/jitenv-<uid>/` (macOS), mode 0600, peer UID checked via `SO_PEERCRED` on Linux and `LOCAL_PEERCRED` on Darwin (see `peer_linux.go` / `peer_darwin.go`). JSON ops: `status`, `is_mapped`, `fetch_env`, `lock`, `reload` (length-prefixed; see `internal/agent/protocol.go`).
3. **Shell hook + `jitenv run`** (`internal/shell/snippets/{bash,zsh}.sh`, `internal/run`) — bash uses `extdebug` + `DEBUG` trap; zsh uses `preexec`. The hook intercepts only commands whose first token resolves to an absolute or `./`/`../` path, calls `jitenv is-mapped`, and on a hit re-runs the command via `jitenv run` which `syscall.Exec`s the file with the merged env (so secrets only ever live in that child's process tree).

### Hook `is-mapped` exit codes (load-bearing)

The bash/zsh snippets switch on the exit code of `jitenv is-mapped`:
- **0** → mapped, route through `jitenv run`.
- **1** → not mapped, run normally.
- **2** → config unreadable. Run normally — no env injection, no warning. Treated like an unmapped path; only `JITENV_HOOK_DEBUG=1` reveals it.

`jitenv is-mapped` reads the config directly and never contacts the agent, so exit 2 always means a missing/malformed `config.toml`, never an agent-down condition. The agent-down UX (red countdown, "Press Enter to skip, Ctrl+C to abort") lives in `internal/agentwarn/agentwarn.go` and only fires inside `jitenv run` / the cwd_glob shim — i.e. *after* `is-mapped` returned 0.

See `internal/cli/ismapped.go` for the exit-code contract and `bash.sh` for the dispatch.

### Source plugin model

`pkg/source` defines the public `Source` interface (`Name`, `Validate`, `Fetch`) and `Constructor`. Each backend in `internal/sources/<name>` registers itself via `init()` calling `sources.Register(...)`. `internal/sources/builtin` blank-imports them so the binary ships them all; that package is itself blank-imported by `cmd/jitenv/main.go` — adding a new source means adding it under `internal/sources/<name>` AND to `internal/sources/builtin/builtin.go`.

The `local` source is special: it has no params and reads from `cfg.Secrets`, which the agent's resolver injects post-construction via a `SetBags(...)` type assertion (`bagSink` interface in `internal/agent/resolver.go`). Plaintext bag values therefore never leave agent memory.

### Config + crypto

`internal/config` owns the on-disk schema (`Config`, `Mapping`, `VarRef`, `SourceConfig`). `internal/crypto`:
- Argon2id KDF (time=3, mem=64MiB, threads=4) with per-config salt.
- Sensitive fields are stored as `enc:v1:<base64(nonce ‖ XChaCha20-Poly1305(plaintext))>` envelopes.
- `Meta.Verify` is a sentinel envelope decrypted at unlock to verify the passphrase.
- `DecryptStringsInPlace` walks `map[string]any` and replaces every envelope string — that's how arbitrary `[sources.<name>.params]` blocks get decrypted without per-source schema knowledge.

Saves go through `config.AtomicSave` (sibling tempfile + rename, mode 0600). The TUI calls `pingAgentReload` after a successful save so a running agent picks up the new config without `lock`/`unlock`.

### Mapping lookup

`config.Index` (built once per resolver) splits mappings into an exact-path map plus a slice of glob entries (matched with `bmatcuk/doublestar/v4`). `Lookup` returns matching `VarRef`s in declaration order: exact first, then each matching glob; later entries with the same env var name win.

A `VarRef` with empty `Name` means "expand the whole bag" — every key in the source's response becomes its own env var. `Name == ""` AND `Key != ""` is invalid (rejected by `Config.Validate`).

### TUI architecture (`internal/tui`)

Bubble Tea with a screen-stack `rootModel`. Each screen implements `Init / Update / View / Title / Status`. Screens push/pop via `pushMsg` / `popMsg` / `popUntilMsg`. UI pattern is uniform: every list page has a `< Create New … >` sentinel at the top and selecting an existing row opens a popup menu (`popup_menu.go`). Renames cascade: changing a bag/key/source name rewrites every `VarRef` that referenced it (see `internal/tui/references.go`).

### Shell hook installer

`internal/shell/install.go` is more than just appending one line: for bash it also walks `.bash_profile` → `.bash_login` → `.profile` and adds a guarded `. ~/.bashrc` line if none of them already source it (login shells otherwise skip `~/.bashrc`). zsh sources `~/.zshrc` for both interactive and login, so no second file is touched. `jitenv hook install` is idempotent. `unlock.go:warnIfHookMissing` flags both "no hook line" and "hook line present but login chain doesn't load it".

## Conventions worth knowing

- **Master key handling.** The key is `defer zeroBytes(...)`'d everywhere it lives outside the agent. Don't print it, don't pass it as a CLI flag (it's an inherited fd in `SpawnDaemon`). New code that touches the key should follow the same zero-on-defer pattern.
- **Cobra layout.** Commands are constructed by `new<Name>Cmd()` functions and aggregated in `internal/cli/subcommands.go`. To add a top-level command, append it there. The hidden `__agent` command is the daemon entrypoint — it is not a user-facing command.
- **Config path resolution.** Unix: `JITENV_CONFIG` > `$XDG_CONFIG_HOME/jitenv/config.toml` > `~/.config/jitenv/config.toml`. Windows: `JITENV_CONFIG` > `%LOCALAPPDATA%\jitenv\config.toml` > `%USERPROFILE%\AppData\Local\jitenv\config.toml`. The legacy roaming `%APPDATA%\jitenv\config.toml` location is consulted as a read-only fallback when no LOCALAPPDATA config exists, so existing installs upgrade without breakage. Always go through `config.Resolve` rather than reconstructing.
- **Agent paths.** Unix: `$XDG_RUNTIME_DIR/jitenv/{agent.sock,agent.pid,agent.log}`, falling back to `/tmp/jitenv-<uid>/`. Windows: `%LOCALAPPDATA%\jitenv\{agent.pid,agent.log}` with the socket field overloaded to the pipe name `\\.\pipe\jitenv-<sid>`. Use `agent.DefaultPaths()`.
- **No remote source UI.** AWS Secrets Manager is compiled in and works at runtime, but the TUI's "Remote Sources" page is currently hidden. Existing `[sources.*]` entries in `config.toml` keep working.
