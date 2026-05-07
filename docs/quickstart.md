# Quickstart

From zero to "my deploy script has its env vars" in about a minute.

## 1. Install

Pick one. The release artifact path is the simplest if you're on Linux:

```sh
# Debian / Ubuntu (replace with the real version + arch)
curl -LO https://github.com/Galvill/jitenv/releases/latest/download/jitenv_X.Y.Z_linux_amd64.deb
sudo dpkg -i jitenv_X.Y.Z_linux_amd64.deb

# Or build from source
go install github.com/gv/jitenv/cmd/jitenv@latest
```

## 2. Wire up the shell hook (once per machine)

```sh
jitenv hook install      # appends one line to ~/.bashrc or ~/.zshrc
exec $SHELL              # or open a new terminal
```

Idempotent — re-running it does nothing if the line is already
there. See [shell-hook.md](shell-hook.md) for what's actually being
installed.

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

- **kind** → `path`
- **target** → e.g. `/home/me/scripts/deploy.sh` (or pick `glob`
  and use `~/work/**/*.sh`)
- **variables** → tick the keys you want injected. Tick the **bag**
  itself to inject every key in it under its own name (the
  "expand the whole bag" mode).

Save (Ctrl+S, or whatever the footer hints). Quit the TUI.

## 5. Unlock the agent and run

```sh
jitenv unlock            # passphrase prompt; starts the agent
./scripts/deploy.sh      # the hook intercepts; env vars are in scope
echo "$STRIPE_SK"        # empty in your shell — it never had the var
```

That's it. The agent stays up until you `jitenv lock` or the idle
timeout expires; you don't re-enter the passphrase per command.

## What's next

- [concepts.md](concepts.md) — the model. Read this before doing anything
  fancy with globs or whole-bag expansion.
- [shell-hook.md](shell-hook.md) — exit-code contract, login-shell wiring,
  debug flags.
- [security.md](security.md) — what jitenv protects against and what it
  doesn't.
- [troubleshooting.md](troubleshooting.md) — when the hook isn't firing,
  the agent won't start, etc.
- [tui.md](tui.md) — full TUI walkthrough.
- [source-plugins.md](source-plugins.md) — adding a new secret backend.
