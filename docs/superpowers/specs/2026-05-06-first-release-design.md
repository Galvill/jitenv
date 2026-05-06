# jitenv v0.1 release pipeline — design

**Date:** 2026-05-06
**Branch:** `release/v0.1-prep`
**Status:** Approved for implementation planning

## 1. Goal

Stand up a reproducible, automated release pipeline so that merging a release PR to `main` produces a signed, multi-arch GitHub Release with `.deb`, `.rpm`, and tarball artifacts, with no manual `goreleaser` invocations and no human-managed signing keys.

## 2. Scope

### In scope (v0.1)

- Linux only — `linux/amd64` and `linux/arm64`.
- Build, test, vet, format, and lint checks running on every PR and on every push to `main`.
- GoReleaser-driven artifacts: per-arch `.tar.gz`, `.deb`, `.rpm`, plus a `SHA256SUMS` file.
- Cosign keyless signature of `SHA256SUMS` (OIDC against the GitHub Actions identity).
- release-please with Conventional Commits opening "release vX.Y.Z" PRs that, when merged, push a `vX.Y.Z` tag.
- That tag triggers the release workflow which builds and uploads the GitHub Release.

### Explicitly out of scope (v0.1)

- macOS support. The agent uses `SO_PEERCRED` (Linux-specific), `XDG_RUNTIME_DIR`, and a Linux double-fork daemon. A Mac port is a code change, not a packaging task; deferred.
- Hosted apt/yum repositories. Users `wget` the `.deb`/`.rpm` from the Release page and install with `dpkg -i` / `rpm -i`.
- Homebrew, Snap, AUR, Windows, Docker images.
- Hardware-key-managed GPG signing. Cosign keyless replaces it.

## 3. Versioning model

- The current `internal/cli/root.go` constant `const Version = "0.1.0-dev"` is replaced with `var Version = "dev"`. The build-time value is injected via `-ldflags "-X github.com/gv/jitenv/internal/cli.Version=…"`.
- Default value `"dev"` is what plain `go build` and `go install` emit, which is honest about provenance.
- GoReleaser builds inject the tag (`v0.1.0` → `0.1.0`), short commit, and ISO date.
- release-please owns the bump. With `bump-minor-pre-major: true` set:
  - `fix:` / `perf:` → patch bump (0.1.0 → 0.1.1).
  - `feat:` → minor bump (0.1.0 → 0.2.0).
  - `feat!:` or `BREAKING CHANGE:` footer → minor bump while pre-1.0 (the flag's only effect: breaking changes downgrade from major to minor while major-version is 0). Once we cut `v1.0.0`, the flag is removed and breaking changes bump major as usual.
- The first release will be `v0.1.0`, seeded by `release-please-manifest.json`.

## 4. Repository additions

```
.github/
├── workflows/
│   ├── ci.yml
│   ├── release-please.yml
│   └── release.yml
└── release-please-config.json
.golangci.yml
.goreleaser.yaml
release-please-manifest.json
docs/superpowers/specs/2026-05-06-first-release-design.md
```

`Makefile` gains:

- `lint:` → `golangci-lint run`
- `release-snapshot:` → `goreleaser release --snapshot --clean`

## 5. CI workflow — `.github/workflows/ci.yml`

Triggers: `pull_request`, `push` on `main` and `release/**`.

Single job on `ubuntu-latest`, Go version pinned to the minor declared by `go.mod` (`1.25.x`).

Steps in order:

1. `actions/checkout@v4`
2. `actions/setup-go@v5` with module cache enabled
3. `go mod download && go mod verify`
4. `gofmt -d -l .` — fail if any output is produced
5. `go vet ./...`
6. `golangci/golangci-lint-action@v6` reading `.golangci.yml`
7. `go test -race -covermode=atomic ./...`
8. `goreleaser/goreleaser-action@v6` with `args: release --snapshot --clean --skip=publish,sign` — proves the release config still builds; no upload, no signing

The slow e2e tests under `internal/run` and `internal/shell` (which `go build` a real binary into a temp config) are run as part of step 7. If they fail on a clean GitHub-hosted runner, that is treated as a real bug to fix in this branch — not papered over by skipping.

## 6. Lint config — `.golangci.yml`

Linters enabled (curated, low-false-positive set):
`errcheck`, `govet`, `ineffassign`, `staticcheck`, `unused`, `gofmt`, `goimports`, `misspell`.

No issue exclusions to start. The first run on the existing codebase will likely produce findings; they get fixed (or `//nolint:linter // reason`-suppressed where the suppression is justified) in a single "lint cleanup" commit before CI is required-green.

## 7. release-please — `.github/workflows/release-please.yml`, `release-please-config.json`, `release-please-manifest.json`

`release-please-manifest.json`:

```json
{ ".": "0.1.0" }
```

`release-please-config.json`:

- Single Go package at the repository root.
- `release-type: go`
- `bump-minor-pre-major: true`
- `changelog-sections`:
  - visible: `feat`, `fix`, `perf`, `revert`, `docs`
  - hidden: `chore`, `style`, `refactor`, `test`, `build`, `ci`
- No `extra-files` entry. The version visible to a running binary comes exclusively from ldflags driven by the git tag, so there is no in-source constant for release-please to keep in sync. Fewer moving parts.

`release-please.yml`:

- Trigger: `push` on `main`.
- Permissions: `contents: write`, `pull-requests: write`.
- Step: `googleapis/release-please-action@v4` using `GITHUB_TOKEN` by default.

**Known token caveat:** if the repository later adopts a branch protection rule that forbids the `github-actions[bot]` from opening PRs, release-please will silently fail to open the release PR. The mitigation is a fine-grained PAT stored as a `RELEASE_PLEASE_TOKEN` secret. We do not pre-emptively add this for v0.1; we add it only if the default token is rejected.

## 8. GoReleaser config — `.goreleaser.yaml`

- `version: 2`
- `before.hooks`: `go mod download`
- `builds`: one entry
  - `id: jitenv`
  - `main: ./cmd/jitenv`
  - `binary: jitenv`
  - `env: [CGO_ENABLED=0]`
  - `goos: [linux]`
  - `goarch: [amd64, arm64]`
  - `flags: [-trimpath]`
  - `ldflags`: `-s -w -X github.com/gv/jitenv/internal/cli.Version={{.Version}} -X github.com/gv/jitenv/internal/cli.Commit={{.ShortCommit}} -X github.com/gv/jitenv/internal/cli.Date={{.Date}}`
- `archives`:
  - `format: tar.gz`
  - `name_template: "{{ .Binary }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"`
  - `files`: `LICENSE`, `README.md`, `internal/shell/snippets/bash.sh`, `internal/shell/snippets/zsh.sh`
- `nfpms`:
  - `id: jitenv-pkg`
  - formats: `[deb, rpm]`
  - `package_name: jitenv`
  - `vendor`, `homepage`, `maintainer`, `description`, `license` populated
  - `bindir: /usr/bin`
  - no postinst — installing the package never modifies a user's shell config; that is opt-in via `jitenv hook install`
- `checksum.name_template: SHA256SUMS`
- `signs`:
  - one entry, `cmd: cosign`, `signature: ${artifact}.sig`, `certificate: ${artifact}.pem`
  - `args: ["sign-blob", "--output-signature=${signature}", "--output-certificate=${certificate}", "${artifact}", "--yes"]`
  - `artifacts: checksum` (sign the SHA256SUMS, not every binary)
- `release`:
  - `github`
  - `draft: false`
  - `prerelease: auto`
  - changelog body sourced from release-please's CHANGELOG entry

## 9. Release workflow — `.github/workflows/release.yml`

- Trigger: `push` of tag matching `v*`.
- Permissions: `contents: write`, `id-token: write` (cosign OIDC).
- Steps:
  1. `actions/checkout@v4` with `fetch-depth: 0` (so GoReleaser sees full tag history)
  2. `actions/setup-go@v5`
  3. Install `cosign` via `sigstore/cosign-installer@v3`
  4. `goreleaser/goreleaser-action@v6` with `args: release --clean` and `GITHUB_TOKEN` env

No long-lived secrets. Cosign uses ephemeral OIDC.

## 10. Verification block in release notes

The release-please CHANGELOG header gets a short "Verifying" snippet appended via the GoReleaser release body template:

```sh
sha256sum -c SHA256SUMS
cosign verify-blob \
  --certificate-identity-regexp "^https://github.com/Galvill/jitenv" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --signature SHA256SUMS.sig --certificate SHA256SUMS.pem SHA256SUMS
```

## 11. Build / land sequence

The implementation plan should land changes in this order to keep `main` green at every step:

1. Branch `release/v0.1-prep` created (already done as part of this design phase).
2. This spec committed and pushed.
3. `Version` ldflags refactor; `Makefile` adds `lint:` and `release-snapshot:` targets. Verify locally with `make build && ./bin/jitenv version`.
4. `.golangci.yml` added; lint cleanup commit fixes whatever the first run reports.
5. `.github/workflows/ci.yml` added. CI required to be green on this branch.
6. `.goreleaser.yaml` added; `make release-snapshot` validates locally before pushing.
7. `release-please-config.json`, `release-please-manifest.json`, `.github/workflows/release-please.yml`.
8. `.github/workflows/release.yml`.
9. Open PR `release/v0.1-prep` → `main`. Merge.
10. release-please opens its first "release v0.1.0" PR. Merging it tags `v0.1.0`, which fires `release.yml` and produces the first GitHub Release.

## 12. Risks and explicit assumptions

- **Lint cleanup is an unknown-size commit.** The first `golangci-lint run` against the existing codebase may surface either zero issues or several dozen. The plan budgets a dedicated commit for this; if the count is large enough to dwarf other release-prep work it is split into its own PR ahead of the release-prep PR rather than allowed to grow scope.
- **e2e tests on GitHub-hosted runners.** `internal/run` and `internal/shell` shell out to `go build` and exercise a real Unix-domain-socket flow. `agent.DefaultPaths()` already falls back to `/tmp/jitenv-<uid>` when `XDG_RUNTIME_DIR` is absent, so socket placement should work, but the bash-hook tests assume a working `bash` with `extdebug`. If the GH runner's environment trips them, the failure is fixed in this branch — not skipped — because the whole point of the release pipeline is to certify that the binary and the hook actually work.
- **release-please token.** Default `GITHUB_TOKEN` is assumed sufficient. A PAT escape hatch is documented but not provisioned.
- **Cosign keyless verification requires Fulcio/Rekor reachability.** Users on air-gapped networks who want to verify signatures will need a different scheme. Not a v0.1 problem.
- **No package signing for `.deb`/`.rpm` themselves.** Only `SHA256SUMS` is signed. Distribution-grade `.deb`/`.rpm` signing (`debsigs`, `rpmsign --addsign`) is a v0.2 question if/when we host repos.
