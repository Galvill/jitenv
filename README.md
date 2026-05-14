# jitenv

Just-in-time environment variable loader. Holds your secrets in an
encrypted config and injects them into a configured file's process
tree only — never into the parent shell.

```sh
jitenv unlock
./scripts/deploy.sh        # mapped — env vars appear inside the script
echo "$DATABASE_URL"       # empty in your shell — it never saw the value
```

## Why

`.env` files and `direnv` put secrets in **every** process you
launch from a directory. That's fine for low-stakes development,
risky for credentials with real blast radius. jitenv narrows the
exposure to the one process that actually needs the value:

- Encrypted at rest (XChaCha20-Poly1305 + Argon2id).
- A per-user agent holds the master key in memory only.
- Per-file mappings (path or glob); a shell hook intercepts mapped
  commands and re-execs them through `jitenv run`.
- Pluggable sources: `local` (encrypted bags in `config.toml`) and
  AWS Secrets Manager.

## Website

[jitenv.com](https://jitenv.com) — project landing page with overview,
download links, and contact info. Served via GitHub Pages from the
[`/docs`](docs/) folder on `main`; edits land through normal pull
requests.

## Documentation

- [Quickstart](docs/quickstart.md) — install → unlock → mapping → run.
- [Concepts](docs/concepts.md) — sources, bags, mappings, `VarRef`
  semantics, glob behaviour.
- [Shell hook](docs/shell-hook.md) — exit-code contract, login-shell
  wiring, debug flags.
- [Security model](docs/security.md) — threat model, key handling,
  socket access, what's *not* protected.
- [TUI walkthrough](docs/tui.md) — `jitenv config` end to end.
- [Troubleshooting](docs/troubleshooting.md) — hook silent, agent
  unreachable, permission denied.
- [Source plugins](docs/source-plugins.md) — adding a new backend.
- [Releasing](docs/RELEASING.md) — how releases are cut and verified.
- Example config: [docs/examples/local.toml](docs/examples/local.toml).

## Install

### From a release artifact (Linux)

```sh
# Debian / Ubuntu
sudo dpkg -i jitenv_X.Y.Z_linux_amd64.deb

# Fedora / RHEL / openSUSE
sudo rpm -i jitenv_X.Y.Z_linux_amd64.rpm
```

The package's post-install prints a one-liner reminder. Activate the
shell hook **once, as your normal user** (not as root):

```sh
jitenv hook install
exec $SHELL
```

`jitenv hook install` is idempotent — re-running it (or re-installing
the package) won't duplicate lines. Removing the package leaves the
hook line in place; the `preremove` script tells you how to delete
it manually.

### From source

```sh
go install github.com/gv/jitenv/cmd/jitenv@latest
# or
git clone https://github.com/gv/jitenv && cd jitenv && make install

jitenv hook install
```

### Homebrew

A formula is planned alongside the macOS port (see
[#13](https://github.com/Galvill/jitenv/issues/13)). When it lands,
the formula will print caveats with the same `jitenv hook install`
command — Homebrew formulae do not modify a user's shell rc files.

## Commands

```
jitenv config              Open the interactive TUI
jitenv config init         Non-interactive: create a fresh encrypted config
jitenv config show         Print the decrypted config to stdout
jitenv config validate     Parse + structural check
jitenv unlock              Prompt passphrase, start agent
jitenv lock                Stop the agent
jitenv status              Agent status
jitenv run <file>          Fetch env, exec file
jitenv is-mapped <file>    Exit 0 if file is mapped (used by shell hook)
jitenv sources list        Sources defined in your config
jitenv sources types       Source types compiled in
jitenv sources test <n>    Run Validate() against a configured source
jitenv hook bash|zsh|powershell  Print shell hook for eval
jitenv hook install              Append the activation line to your rc file
jitenv hook status               Show whether the hook is wired up
```

## Limitations

- Supported platforms: Linux, macOS, and Windows (PowerShell 7+).
  Linux uses `SO_PEERCRED` + `XDG_RUNTIME_DIR`; macOS uses
  `LOCAL_PEERCRED` + `$TMPDIR`; Windows uses named pipes with token-SID
  peer auth and a `%LOCALAPPDATA%\jitenv` runtime dir. The agent's
  `Setsid` double-fork on Unix has a Windows analogue using
  `CREATE_NO_WINDOW | DETACHED_PROCESS`.
- On Windows the path/glob shell hook (`extdebug` + `DEBUG` / `preexec`)
  is replaced by `cwd_glob` wrapper-shim `.ps1` files invoked via the
  PowerShell prompt override — see `jitenv hook powershell`. The
  bash/zsh hook with absolute-path interception is Unix-only.
- macOS release binaries are not Apple-notarized; first run requires
  `xattr -d com.apple.quarantine ./jitenv` or right-click → Open.
  Windows release binaries are not Authenticode-signed; SmartScreen
  may warn on first run.
- The bash/zsh shell hook only intercepts commands whose first token
  is an absolute or `./`-relative path — not bare PATH lookups.
- Single agent per user; multiple terminals share one unlocked instance.
- TUI requires a TTY; for scripted setup use `jitenv config init` then
  re-run interactively.

## Building from source

```sh
make build           # ./bin/jitenv
make test            # go test ./...
make install         # installs to $PREFIX/bin (default ~/.local/bin)
```

Go 1.25+ required (see `go.mod`).

## License

[MIT](LICENSE).
