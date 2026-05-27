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

### Homebrew (macOS and Linux)

```sh
brew install Galvill/jitenv/jitenv
```

Distributed as a Homebrew **cask** that downloads the goreleaser
tarball for your arch. macOS binaries are Developer ID code-signed
and notarized, so Gatekeeper accepts them without a quarantine
override. After install, activate the shell hook **once** as your
normal user:

```sh
jitenv hook install
exec $SHELL
```

Homebrew never modifies your shell rc files on its own.

### Chocolatey (Windows)

```powershell
choco install jitenv
```

A downloader package that fetches the official `windows_amd64` release
zip and verifies its SHA256 before extracting; Chocolatey shims both
`jitenv.exe` and `jitenv-tui.exe` onto `%PATH%`. `choco upgrade jitenv`
picks up new releases. After install, activate the PowerShell hook
**once**:

```powershell
jitenv hook install
# open a new pwsh tab — the hook is live
```

The first run of `jitenv.exe` may trip SmartScreen because the binary
is not yet Authenticode-signed (tracked in
[#39](https://github.com/Galvill/jitenv/issues/39)) — `Unblock-File`,
or right-click → Properties → Unblock, clears it. PowerShell 7+ is
required for the hook.

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
jitenv clone <https-url>   Clone a repo and store its PAT in an encrypted bag
jitenv sources list        Sources defined in your config
jitenv sources types       Source types compiled in
jitenv sources test <n>    Run Validate() against a configured source
jitenv hook bash|zsh|powershell  Print shell hook for eval
jitenv hook install              Append the activation line to your rc file
jitenv hook status               Show whether the hook is wired up
```

## Clone a private repo (`jitenv clone`)

Captures the auth token once, stores it encrypted, and wires a
mapping so `git` commands inside the clone target see it via
`GIT_ASKPASS` — the token never lands in `.git/config`, `~/.netrc`,
or any shell other than the one running `git`.

```sh
jitenv clone https://github.com/acme/private-repo
# Passphrase: ****
# Token (PAT): ****************
# Cloning into 'private-repo'...
# done. mapped /home/user/private-repo/** → git (token bag: acme-private-repo)
# Add more mappings or secrets for this repo? [y/N]
```

After the clone:

- A new `[secrets.acme-private-repo]` block (encrypted) holds the
  PAT.
- A new `cwd_glob` mapping covers the cloned tree with
  `commands = ["git"]`, injecting `JITENV_GIT_TOKEN` (from the bag)
  and `GIT_ASKPASS` (a per-user shim that returns the token to git).
- Answering `y` to the post-clone prompt opens the TUI on a
  Mappings → Create New screen pre-filled with the cloned path, so
  you can add more mappings to the same repo without re-typing the
  passphrase. `--no-prompt` skips it; non-TTY stdin auto-skips.

Limitations:

- HTTPS only in this release. SSH key support is a follow-up.
- The askpass shim lives at `$XDG_DATA_HOME/jitenv/bin/git-askpass.sh`
  (Unix) or `%LOCALAPPDATA%\jitenv\bin\git-askpass.bat` (Windows).
  Moving the jitenv binary after a clone will break the shim; re-run
  `jitenv clone` (or regenerate the file manually) to refresh it.

## Version check

On hook load each shell tab spawns a fire-and-forget background
fetch of `api.github.com/repos/Galvill/jitenv/releases/latest`,
caches the tag at `$XDG_CACHE_HOME/jitenv/version_check.json`
(Linux/macOS) or `%LOCALAPPDATA%\jitenv\version_check.json`
(Windows), and prints a one-line stderr notice the next time you
open a shell if a newer release is available. The fetch is rate-
limited to once every 24 hours per user, no telemetry headers are
sent, and only `tag_name` is read from the response. Opt out via:

- `JITENV_NO_VERSION_CHECK=1` for a single shell session,
- `[agent] version_check = false` in `config.toml` for the user,
- the `CI` env var (auto-skip — matches every mainstream CI), or
- a non-TTY stderr (piped output, log capture).

Dev builds (`go install` / plain `go build` with no ldflags) skip
the check entirely — there is no upgrade story for snapshots.

## Limitations

- Supported platforms: Linux, macOS, and Windows (PowerShell 7+).
  Linux uses `SO_PEERCRED` + `XDG_RUNTIME_DIR`; macOS uses
  `LOCAL_PEERCRED` + `$TMPDIR`; Windows uses named pipes with token-SID
  peer auth and a `%LOCALAPPDATA%\jitenv` runtime dir. The agent's
  `Setsid` double-fork on Unix has a Windows analogue using
  `CREATE_NO_WINDOW | DETACHED_PROCESS`.
- All three mapping kinds (`path`, `glob`, `cwd_glob`) work on every
  supported shell. Implementation differs per shell:
  - bash: `extdebug` + `DEBUG` trap intercepts absolute / `./`-relative
    paths; `cwd_glob` is driven by wrapper symlinks in a per-shell PATH
    entry, reconciled on every prompt.
  - zsh: an `accept-line` zle widget does the same path interception;
    `cwd_glob` again uses wrapper symlinks reconciled per prompt.
  - PowerShell 7+: a PSReadLine `Enter` chord handler intercepts paths;
    `cwd_glob` uses `.ps1` wrappers in a per-shell PATH entry,
    reconciled by the `prompt` override. PSReadLine is the default
    module in pwsh 7+; without it, `path`/`glob` interception silently
    no-ops and `cwd_glob` still works.
- Windows release binaries are not Authenticode-signed; SmartScreen
  may warn on first run.
- The shell hook only intercepts commands whose first token is an
  absolute or `./`-relative path — not bare PATH lookups (those are
  routed via `cwd_glob` wrappers instead).
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
