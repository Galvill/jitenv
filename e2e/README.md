# jitenv e2e harness

Containerised end-to-end stack and scenario runner for jitenv. The
harness exists so an AI coding agent (Claude) — or a human — can
reproduce the unlock → hook → run → fetch loop against real source
backends and surface failures from rich on-disk diagnostics.

## Quick start

```sh
make e2e-up                                  # build images, start stack, wait for healthchecks
make e2e-run SCENARIO=unlock-and-run-local.yaml
make e2e-run SCENARIO=localstack-fetch.yaml
make e2e-run SCENARIO=vault-fetch.yaml
make e2e-down                                # tear stack down (keeps volumes)
make e2e-down-hard                           # tear stack down + drop volumes
```

Failed runs leave a self-contained artefact directory under
`e2e/runs/<scenario>-<timestamp>/`. That directory IS the bug report
— the harness never holds in-memory state past `summary.json`.

## Stack layout

`e2e/docker-compose.yml` runs:

| service          | role                                                                                                  |
| ---------------- | ----------------------------------------------------------------------------------------------------- |
| `debian`         | Debian bookworm-slim (glibc); installs jitenv from `dist/*.deb` (covers nfpms layout + postinstall)   |
| `fedora`         | Fedora 40 (glibc); installs jitenv from `dist/*.rpm` (covers rpm layout + postinstall)                |
| `alpine`         | Alpine 3.20 (musl); extracts `dist/*.tar.gz` into the deb/rpm layout (covers archive contents)        |
| `alpine-source`  | Alpine 3.20 (musl); builds jitenv from `HEAD` so the source build path against musl stays exercised   |
| `localstack`     | LocalStack 3.x with Secrets Manager, seeded with one JSON secret                                      |
| `vault`          | HashiCorp Vault in dev mode (deterministic root token `dev-root`, no host port, KV v2 at `secret/`)   |

The three install-from-artefact services (`debian`, `fedora`, `alpine`)
depend on `dist/` being populated by `make e2e-build-artifacts`, which
runs `goreleaser release --snapshot --clean --skip=publish,sign` and
records the source HEAD in `dist/.snapshot-stamp`. The stamp's mtime
is compared against the source tree by Make, so subsequent
`make e2e-up` cycles skip the rebuild when nothing relevant has
changed. Force a rebuild by deleting the stamp.

Each distro container has:

- A non-root `jitenv` user (uid 1000) with a writable home and `~/.config/jitenv/`.
- `/tmp` mounted tmpfs, owned by the test user — `agent.DefaultPaths()`
  falls back to `/tmp/jitenv-1000/` so this is the agent's runtime dir.
- `bash`, `zsh`, `ca-certificates`, `curl`, `tini`, `coreutils`, `procps`.
- `/usr/local/bin/jitenv-e2e-seed` — fixture-config generator.
- `/usr/local/bin/jitenv-e2e-unlock` — non-interactive replacement for
  `jitenv unlock` (see "Driving unlock" below).
- `/usr/local/bin/jitenv-e2e-reload` — sends `OpReload` to a running
  agent; the mid-session-reload scenario uses it as the harness
  analogue of the TUI's post-save `pingAgentReload`.

## Run-dir layout

Every scenario produces `e2e/runs/<name>-<UTC-timestamp>/`:

```
meta.json                  scenario, service, verdict, started/ended timestamps
summary.json               full per-step report (kind, status, exit, duration, error)
steps/
  000-<step-name>/cmd      what was executed (or the assertion meta)
                  stdout   captured stdout (exec steps only)
                  stderr   captured stderr (exec steps only)
                  exit     exit code as a single integer
  001-<step-name>/...
teardown/
  agent.log                tail of /tmp/jitenv-1000/agent.log inside the service
  config.toml              encrypted config state at scenario end
  compose-ps.txt           docker compose ps snapshot
  ps-aux.txt               in-container process listing at teardown time
```

Acceptance criteria in scenarios reference paths in this layout — do
not rename or restructure without updating callers.

## Scenario format

Scenarios live under `e2e/scenarios/<name>.yaml`. The shape is
deliberately small: top-level scenario metadata + a flat list of
steps. Exactly one action per step.

```yaml
name: my-scenario
service: debian          # docker-compose service to exec into
user: jitenv             # default user for every step (override per-step with `user:`)
steps:
  - name: do-thing
    exec: echo hello
  - name: assert-ok
    assert_exit_code: 0
  - name: assert-out
    assert_stdout_contains: "hello"
  - name: wait-socket
    wait_for_file: /tmp/jitenv-1000/agent.sock
    timeout: 10s
```

### Step actions (one per step)

| field                         | semantics                                                       |
| ----------------------------- | --------------------------------------------------------------- |
| `exec`                        | run a `bash -c` command inside the service                      |
| `wait_for_file`               | poll `test -e <path>` inside the service until present          |
| `assert_exit_code: <int>`     | the previous exec step exited with this code                    |
| `assert_stdout_contains`      | substring match against stdout                                  |
| `assert_stdout_equals`        | exact match (trailing `\n` trimmed)                             |
| `assert_stdout_not_contains`  | negative substring match (used for "no leak" assertions)        |
| `assert_stderr_contains`      | substring match against stderr                                  |

### Step modifiers

| field            | semantics                                                                  |
| ---------------- | -------------------------------------------------------------------------- |
| `target`         | name of an earlier exec step to assert against (default: last exec)        |
| `service`        | override the scenario service for this step                                |
| `user`           | override the scenario user for this step                                   |
| `env`            | extra `-e KEY=VAL` for the docker exec                                     |
| `stdin`          | string fed to the command's stdin                                          |
| `timeout`        | per-step timeout (Go duration syntax: `30s`, `5m`, …)                      |

### Why `target`

`jitenv run` exits with the inner script's exit code, which can be
nonzero for legitimate reasons. `assert_exit_code` against the most
recent exec is the common case; `target: <step-name>` lets a later
assertion refer back to an earlier step explicitly.

## Adding a new distro

The default model is **install from the goreleaser artefact**, not
build-from-source. We only keep one source-build image
(`alpine-source`) so the musl Go build path stays exercised against
HEAD; everything else mirrors what real users install.

1. Pick the artefact format your distro consumes from `dist/` after
   `make e2e-build-artifacts`:
   - `.deb` for Debian / Ubuntu derivatives — `dpkg -i`
   - `.rpm` for Fedora / RHEL / openSUSE derivatives — `rpm -i` (or
     `dnf install ./pkg.rpm`)
   - `.tar.gz` for distros without a goreleaser nfpms format
     (Alpine, Arch); extract and lay the contents out to match the
     deb/rpm paths so install-layout scenarios stay portable
2. Create `e2e/Dockerfile.<distro>` modelled on the closest existing
   one. Use a small `helper-build` stage to compile
   `e2e/seed` and `e2e/cmd/unlock` (those are NOT shipped by the
   package). Then in the runtime stage:
   - `COPY dist/jitenv_*_linux_${TARGETARCH}.<ext> /opt/pkg/`
   - install via the distro's package manager
   - keep the artefact under `/opt/pkg/` so install-layout scenarios
     can re-run `dpkg -i` / `rpm -i` / `rpm -e` at runtime to capture
     postinstall / preremove output without rebuilding the image
3. The runtime image MUST install:
   - `bash`, `zsh`, `ca-certificates`, `curl`, `tini`, `coreutils`,
     `procps` (or `procps-ng` on Fedora)
   - `script(1)` — bsdutils on Debian, util-linux on Alpine /
     Fedora — only if a future scenario adds back a PTY-driven step
4. Provision a non-root `jitenv` user (uid 1000) with
   `~/.config/jitenv/` and `~/scripts/` pre-created (mode 0700).
5. Add a `<distro>:` service to `e2e/docker-compose.yml`. Mount
   `/tmp` as tmpfs owned by uid 1000 so `agent.DefaultPaths()` lands
   somewhere fresh per `make e2e-up`.
6. The new service's healthcheck should run `jitenv version` — that
   way a broken artefact fails the stack startup, not the first
   scenario.
7. Add an `install-layout-<distro>.yaml` scenario alongside the
   existing ones if you want layout / postinstall coverage.
8. Bump `service:` in any new functional scenarios to point at it.

If you need a true source-build image (e.g. to validate a new libc
combination at HEAD before the next release), follow the
`alpine-source` template — it has no dependency on `dist/` and is
useful for "does it even compile" smoke tests.

## Adding a new source backend

The fixture generator is `e2e/seed/seed.go`. Today it knows the
`local`, `localstack`, and `vault` variants. To add another:

1. Add a new `apply<Source>` function that mirrors the TUI's save
   shape for that source — see `applyLocalstack` / `applyVault` for
   the encrypted-params pattern (use `crypto.EncryptField` for any
   `Sensitive` schema field).
2. Add a case in the `switch variant` block.
3. If the source needs a backing service (Vault, mock GitHub, …):
   - add it to `docker-compose.yml` with a healthcheck;
   - put init scripts under `e2e/seed/<service>-init/` if the image
     supports a ready-hook (LocalStack does);
   - alternatively, drive setup inline from the scenario via `curl`
     against the service's HTTP API — `vault-fetch.yaml` does this
     because the runner images don't ship a `vault` CLI;
   - add the service as a `depends_on: condition: service_healthy`
     for the distro services that need it.
4. Add a scenario under `e2e/scenarios/` that drives unlock + run
   against a mapped script using the new source.

### Vault scenario notes

The `vault` compose service runs `hashicorp/vault` in dev mode with
a fixed root token (`dev-root`) on the internal compose network only
— no host port mapping. KV v2 is the default mount (`secret/`); the
`vault-fetch.yaml` scenario also enables KV v1 at `kv/` inline via
`POST /v1/sys/mounts/kv` so a single scenario covers both KV
versions. Secrets are pre-seeded by the scenario itself with `curl`
against the Vault HTTP API, keeping seed and assertions
co-located. The seed binary's `vault` variant writes two
`[sources.vault]` blocks (one per KV version) and round-trips the
token through `crypto.EncryptField`, exercising the agent's
`DecryptStringsInPlace` walk over the params block.

## Driving `jitenv unlock` non-interactively

`jitenv unlock` reads its passphrase from `/dev/tty`, which
`docker exec -T` does not provide. The first attempt was a `script(1)`
PTY wrapper, but golang.org/x/term's `ReadPassword` doesn't reliably
consume input piped through a subprocess-allocated PTY (the canonical
line buffer hand-off has timing issues we won't fix in the harness).

Instead the image ships `jitenv-e2e-unlock`, a tiny Go helper that
takes the passphrase as a flag (or on stdin) and otherwise calls the
exact same code path as production unlock — `config.Load`,
`config.DeriveKeyFromMeta`, `agent.SpawnDaemon`. The only thing it
skips is `crypto.PromptPassphrase`. This means scenarios exercise the
KDF (passphrase verification), salt unmarshalling, daemon double-fork,
and key handover via fd 3 — i.e. everything below `unlock` in the
production stack.

```sh
jitenv-e2e-unlock -passphrase e2e-test-pass
# or
printf '%s\n' "$pw" | jitenv-e2e-unlock -passphrase-stdin
```

We deliberately do NOT add a `--passphrase-fd` flag to the real
`jitenv unlock`; the e2e harness is the only consumer that needs
this and it owns its own helper.

## Common failure-mode reads

When a scenario fails, look at these files in order:

1. `summary.json` — which step failed and why (the `error` field).
2. `steps/<failed-step>/{stdout,stderr}` — what the command emitted.
3. `teardown/agent.log` — the agent's own log, captured at scenario
   end. Empty file means the agent never started.
4. `teardown/config.toml` — the on-disk encrypted state. The
   scenario harness does NOT decrypt; if you need to inspect, use
   `jitenv config show` against the file with the e2e passphrase
   `e2e-test-pass`.
5. `teardown/ps-aux.txt` — useful when checking whether the daemon
   is still alive after a `lock` step.

## Out of scope (deferred to follow-ups)

- Mock GitHub, OIDC services
- Arch distro (fedora is now covered)
- Multi-distro coverage of functional scenarios beyond what
  `install-layout-*` already gives — debian is the canonical
  reference for behaviour tests; cross-distro packaging is what the
  install-layout family exists for
- More than one zsh hook scenario
