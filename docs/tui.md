# TUI walkthrough

`jitenv config` is the home screen for everything. The whole UI is
**menu-driven** — every place that references a configured object
(a bag name, a key inside a bag, the path/glob kind for a mapping…)
is selected from a list rather than typed, so identifiers stay
consistent.

The main menu lists three sections; each opens a list-style page:

```
Mappings      (5 defined)
Local secrets (3 bags)
Settings
```

## List + popup pattern

Every list page is the same shape: a `< Create New … >` sentinel at
the top, then existing items. Selecting the sentinel opens an input
popup; selecting an existing item opens a small bordered popup menu
with the available actions:

```
< Create New Bag >                ← Enter opens a "name new bag" input
stripe       (2 keys)
db           (3 keys)             ← Enter opens:  Edit / Rename / Delete / Back
```

The same shape repeats for the bag detail page (keys inside a bag)
and the mappings page.

## Editing a mapping

Selecting a mapping (or `< Create New Mapping >`) drills into a
3-row editor:

```
kind:      path
target:    /home/me/scripts/deploy.sh
variables: 4 selected
```

Enter on each row opens its own popup:

- **kind** — choose `path` or `glob`.
- **target** — type the path or glob.
- **variables** — opens a bag→key tree with checkboxes.

The variable tree shows every local-secret bag with its keys
indented beneath. Ticking the bag includes the whole bag (now and
any future keys); ticking individual keys produces named env vars
per key. While a bag is in "all" mode, the individual key boxes
render dimmed.

## Local secrets

Local-secret values are AEAD-encrypted with the master key and
stored inline in `config.toml` — no extra file, no plaintext on disk.

Renaming a bag, a source, or a key automatically rewrites every
mapping that referenced it, so existing mappings stay valid (this is
the rename cascade in `internal/tui/references.go`).

You can also invoke a mapped file explicitly without the hook:

```sh
jitenv run ./scripts/deploy.sh arg1 arg2
```

## Settings

The Settings page exposes the agent idle timeout. The timeout is
**rolling**: every request bumps `last_seen`, and the agent shuts
down once the gap exceeds the configured duration. Because the
shell hook calls `jitenv is-mapped` on every command, an active
hooked shell counts as continuous activity and keeps the agent
alive indefinitely. An empty / zero value disables the auto-shutdown
loop — the agent stays up until `jitenv lock`.

## Saves auto-reload the running agent

After every save the TUI best-effort pings the running agent
(`OpReload`) so it picks up the new config without `jitenv lock` +
`unlock`. If the agent isn't running, the save just writes the file
— the next `jitenv unlock` reads the latest version.

## Remote sources page

AWS Secrets Manager and GitHub Variables are compiled in but the
"Remote Sources" page is currently hidden in the TUI. Existing
`[sources.*]` entries in `config.toml` keep working at runtime; they
just can't be managed interactively. Tracking issues:
[#16](https://github.com/Galvill/jitenv/issues/16),
[#17](https://github.com/Galvill/jitenv/issues/17).
