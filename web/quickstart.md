# Quickstart

From zero to "my deploy script has its env vars" in about a minute.

Current stable release:
<!-- VERSION:start -->v0.14.0<!-- VERSION:end -->.

## 1. Install

Pick the path for your platform. Pre-built, cosign-signed binaries
ship on every tag via goreleaser.

### Linux — release artifact

```sh
# Debian / Ubuntu
curl -LO https://github.com/Galvill/jitenv/releases/latest/download/jitenv_<!-- ARTIFACT_VERSION:start -->0.14.0<!-- ARTIFACT_VERSION:end -->_linux_amd64.deb
sudo dpkg -i jitenv_<!-- ARTIFACT_VERSION:start -->0.14.0<!-- ARTIFACT_VERSION:end -->_linux_amd64.deb

# Fedora / RHEL / openSUSE
curl -LO https://github.com/Galvill/jitenv/releases/latest/download/jitenv_<!-- ARTIFACT_VERSION:start -->0.14.0<!-- ARTIFACT_VERSION:end -->_linux_amd64.rpm
sudo rpm -i jitenv_<!-- ARTIFACT_VERSION:start -->0.14.0<!-- ARTIFACT_VERSION:end -->_linux_amd64.rpm
```

`amd64` and `arm64` are both published; swap the arch in the filename.

### macOS — Homebrew

```sh
brew install Galvill/jitenv/jitenv
```

A Homebrew cask that downloads the notarized tarball for your arch.
macOS binaries are Apple Developer ID code-signed and notarized, so
Gatekeeper accepts them without a quarantine override.

### Windows — Chocolatey

```powershell
choco install jitenv
```

Fetches the official `windows_amd64` release zip, SHA256-verifies it,
and shims both `jitenv.exe` and `jitenv-tui.exe` onto `%PATH%`.
PowerShell 7+ is required for the hook.

### From source

```sh
go install github.com/gv/jitenv/cmd/jitenv@latest
# or
git clone https://github.com/Galvill/jitenv && cd jitenv && make install
```

## 2. Wire up the shell hook (once per machine)

```sh
jitenv hook install      # appends one line to ~/.bashrc or ~/.zshrc
exec $SHELL              # or open a new terminal
```

On Windows, run the same in PowerShell 7+:

```powershell
jitenv hook install      # adds the activation line to your $PROFILE
```

Idempotent — re-running it does nothing if the line is already
there. For bash the installer also wires the login chain
(`.bash_profile` → `.bash_login` → `.profile`) so login shells still
source `~/.bashrc`. See [shell-hook.md](shell-hook.md) for what's
actually being installed.

## 3. Create a config and add a secret

```sh
jitenv config            # opens the TUI; prompts for a passphrase the first time
```

In the TUI:

1. From the main menu, choose **Local secrets** → `< Create New Bag >`.
2. Name it, e.g. `stripe`.
3. Inside the bag, `< Create New Key >`, e.g. `STRIPE_SK`, paste the
   value. Repeat for as many keys as you need.

## 4. Add a mapping

Back in the main menu, **Mappings** → `< Create New Mapping >`:

- **kind** → `path`, `glob`, or `cwd_glob`.
- **target** → e.g. `/home/me/scripts/deploy.sh` (the **target**
  row opens a file browser so you can pick the file instead of
  typing it), or pick `glob` and use `~/work/**/*.sh`, or `cwd_glob`
  and scope a directory.
- **variables** → tick the keys you want injected. Tick the **bag**
  itself to inject every key in it under its own name (the
  "expand the whole bag" mode).

Save (Ctrl+S, or whatever the footer hints). Quit the TUI. Saving
auto-reloads a running agent — no relock needed.

Prefer the shell? `jitenv clone <https-url>` clones a private repo,
captures its PAT once, stores it encrypted, and wires a `cwd_glob`
mapping so `git` inside the tree authenticates without the token
landing in `.git/config`. See [concepts.md](concepts.md) for the
`cwd_glob` model.

## 5. Unlock the agent and run

```sh
jitenv unlock            # passphrase prompt; starts the agent
./scripts/deploy.sh      # the hook intercepts; env vars are in scope
echo "$STRIPE_SK"        # empty in your shell — it never had the var
```

That's it. The agent stays up until you `jitenv lock` or the idle
timeout expires; you don't re-enter the passphrase per command. If
you run a mapped command while the agent is locked, the hook offers
an inline `[u]`-to-unlock prompt so you can unlock and inject without
leaving the command.

## What's next

- [concepts.md](concepts.md) — the model. Read this before doing anything
  fancy with globs, `cwd_glob`, or whole-bag expansion.
- [shell-hook.md](shell-hook.md) — exit-code contract, login-shell wiring,
  debug flags.
- [security.md](security.md) — what jitenv protects against and what it
  doesn't.
- [troubleshooting.md](troubleshooting.md) — when the hook isn't firing,
  the agent won't start, etc.
- [tui.md](tui.md) — full TUI walkthrough.
- [source-plugins.md](source-plugins.md) — adding a new secret backend.

<!-- VERSION:asof:start -->_Docs current as of v0.14.0._<!-- VERSION:asof:end -->
