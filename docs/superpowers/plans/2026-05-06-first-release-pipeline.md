# jitenv v0.1 release pipeline — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up a Linux-only (amd64+arm64) automated release pipeline for jitenv: PR-gated CI (build/test/lint/snapshot), Conventional-Commits-driven version bumps via release-please, and tag-triggered GoReleaser builds that publish signed `.tar.gz`/`.deb`/`.rpm` artifacts to GitHub Releases.

**Architecture:** The pipeline is three GitHub Actions workflows plus three config files. `ci.yml` runs on every PR. `release-please.yml` runs on every push to `main`, opens a "release vX.Y.Z" PR, and tags `vX.Y.Z` when that PR is merged. The tag fires `release.yml`, which invokes GoReleaser to cross-compile, package, checksum, cosign-sign (keyless OIDC), and upload to a GitHub Release. The binary's runtime version is injected via `-ldflags` so the artifact reports its own provenance.

**Tech Stack:** Go 1.25.x, GoReleaser v2, `goreleaser/goreleaser-action@v6`, `nfpm` (via GoReleaser) for `.deb`/`.rpm`, `googleapis/release-please-action@v4`, `golangci/golangci-lint-action@v6`, `sigstore/cosign-installer@v3`, GitHub Actions OIDC.

**Branch:** `release/v0.1-prep` (already created and checked out).

**Source spec:** `docs/superpowers/specs/2026-05-06-first-release-design.md`.

**Conventional Commits:** Every commit on this branch must follow [Conventional Commits](https://www.conventionalcommits.org). Hidden-from-CHANGELOG types: `chore`, `style`, `refactor`, `test`, `build`, `ci`. Visible: `feat`, `fix`, `perf`, `revert`, `docs`. Use `build:` and `ci:` for the infra work in this plan so the first release notes stay clean.

---

## Task 1: Make `Version` injectable via ldflags, with `Commit` and `Date` companions

Refactor the hard-coded version constant into three `var`s populated at build time, and update the `version` subcommand to print all three. This is the prerequisite for everything else — release artifacts must be able to report what tag they came from.

**Files:**
- Modify: `internal/cli/root.go:7`
- Modify: `internal/cli/version.go` (rewrite)
- Create: `internal/cli/version_test.go`
- Modify: `Makefile:7-8` (build target)

- [ ] **Step 1: Write the failing test for `formatVersion`**

Create `internal/cli/version_test.go`:

```go
package cli

import "testing"

func TestFormatVersion(t *testing.T) {
	cases := []struct {
		name              string
		ver, commit, date string
		want              string
	}{
		{
			name:   "all fields populated",
			ver:    "0.1.0",
			commit: "abc1234",
			date:   "2026-05-06T12:34:56Z",
			want:   "jitenv 0.1.0 (commit abc1234, built 2026-05-06T12:34:56Z)",
		},
		{
			name:   "dev build with empty commit and date",
			ver:    "dev",
			commit: "",
			date:   "",
			want:   "jitenv dev",
		},
		{
			name:   "dev build with commit but no date",
			ver:    "dev",
			commit: "abc1234",
			date:   "",
			want:   "jitenv dev (commit abc1234)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatVersion(tc.ver, tc.commit, tc.date)
			if got != tc.want {
				t.Errorf("formatVersion(%q, %q, %q) = %q, want %q",
					tc.ver, tc.commit, tc.date, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cli -run TestFormatVersion -v`

Expected: FAIL with `undefined: formatVersion`.

- [ ] **Step 3: Refactor `internal/cli/root.go` — `const Version` → `var`s**

Edit `internal/cli/root.go`. Replace line 7:

```go
const Version = "0.1.0-dev"
```

with:

```go
// Build-time injected via -ldflags. Defaults intentionally identify a
// non-release build so plain `go build` / `go install` are honest.
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)
```

- [ ] **Step 4: Rewrite `internal/cli/version.go`**

Replace the entire contents of `internal/cli/version.go`:

```go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print jitenv version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(formatVersion(Version, Commit, Date))
		},
	}
}

func formatVersion(version, commit, date string) string {
	switch {
	case commit != "" && date != "":
		return fmt.Sprintf("jitenv %s (commit %s, built %s)", version, commit, date)
	case commit != "":
		return fmt.Sprintf("jitenv %s (commit %s)", version, commit)
	default:
		return fmt.Sprintf("jitenv %s", version)
	}
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/cli -run TestFormatVersion -v`

Expected: PASS, all three subtests green.

- [ ] **Step 6: Update the `Makefile` `build` target to inject ldflags**

Replace lines 7-8 of `Makefile`:

```makefile
build:
	$(GO) build -trimpath -ldflags "-s -w" -o bin/$(BIN) ./cmd/jitenv
```

with:

```makefile
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X github.com/gv/jitenv/internal/cli.Version=$(VERSION) \
	-X github.com/gv/jitenv/internal/cli.Commit=$(COMMIT) \
	-X github.com/gv/jitenv/internal/cli.Date=$(DATE)

build:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BIN) ./cmd/jitenv
```

- [ ] **Step 7: Verify the local build reports the new version format**

Run: `make build && ./bin/jitenv version`

Expected output looks like (commit hash and date will vary):

```
jitenv bd47aac (commit bd47aac, built 2026-05-06T18:12:00Z)
```

(`bd47aac` is the spec commit on this branch; once a tag exists, `git describe` will print the tag, e.g. `v0.1.0`.)

- [ ] **Step 8: Run the full test suite**

Run: `go test ./...`

Expected: PASS. If a pre-existing test that referenced the old `Version` constant breaks, fix it now — search with: `grep -rn "cli.Version\|0.1.0-dev" --include="*.go" .` and update.

- [ ] **Step 9: Commit**

```bash
git add internal/cli/root.go internal/cli/version.go internal/cli/version_test.go Makefile
git commit -m "refactor(cli): make Version, Commit, Date injectable via ldflags"
git push
```

---

## Task 2: Add `lint` and `release-snapshot` Makefile targets

These don't require the underlying tools to be installed yet — they just declare the entry points so the rest of the plan and CI can rely on them.

**Files:**
- Modify: `Makefile:5` (`.PHONY` line) and append at end of file

- [ ] **Step 1: Update `.PHONY`**

In `Makefile`, replace line 5:

```makefile
.PHONY: build install test fmt vet tidy clean
```

with:

```makefile
.PHONY: build install test fmt vet tidy lint release-snapshot clean
```

- [ ] **Step 2: Append the two new targets at the end of `Makefile`**

Append after the `clean:` rule:

```makefile
lint:
	golangci-lint run

release-snapshot:
	goreleaser release --snapshot --clean --skip=publish,sign
```

- [ ] **Step 3: Verify Makefile parses**

Run: `make -n lint release-snapshot`

Expected: prints `golangci-lint run` and `goreleaser release --snapshot --clean --skip=publish,sign` without "missing separator" or "no rule" errors. The commands won't actually run yet because the tools aren't required for `-n` (dry-run).

- [ ] **Step 4: Commit**

```bash
git add Makefile
git commit -m "build: add lint and release-snapshot Makefile targets"
git push
```

---

## Task 3: Add `golangci-lint` config and run lint cleanup

Add the lint config, install the tool locally, run it, and fix or annotate every finding in a single follow-up commit. The size of the cleanup commit is unknown until the first run.

**Files:**
- Create: `.golangci.yml`

- [ ] **Step 1: Install `golangci-lint` locally**

Run:

```bash
go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.62.2
```

Verify: `golangci-lint version` prints `1.62.2` (or compatible).

If `$GOPATH/bin` (typically `~/go/bin`) is not on `$PATH`, add it for this shell: `export PATH="$PATH:$(go env GOPATH)/bin"`.

- [ ] **Step 2: Create `.golangci.yml`**

This uses the golangci-lint v1.x config schema (matches the pinned `v1.62.2` binary in step 1 and in CI). If you later upgrade to golangci-lint v2.x, run `golangci-lint migrate` to convert.

```yaml
run:
  timeout: 5m
  tests: true

linters:
  disable-all: true
  enable:
    - errcheck
    - govet
    - ineffassign
    - staticcheck
    - unused
    - misspell
    - gofmt
    - goimports

issues:
  max-issues-per-linter: 0
  max-same-issues: 0
```

- [ ] **Step 3: Run lint and capture findings**

Run: `golangci-lint run ./... 2>&1 | tee /tmp/jitenv-lint-first-run.txt`

Expected: a list of zero or more findings. If zero, skip to step 6.

- [ ] **Step 4: Triage each finding**

For each finding in `/tmp/jitenv-lint-first-run.txt`:

- If the finding is a real bug or sloppiness — **fix it**.
- If the finding is a false positive or the cleaner alternative is worse — add `//nolint:<linter> // <one-line justification>` on the offending line. The justification is mandatory; no bare `nolint`.
- If a whole file legitimately needs an exception (rare), add a file-level `//nolint:<linter>` after the package clause.

Common patterns and the right reaction:

| Finding | Reaction |
|---|---|
| `errcheck`: ignored error from `defer f.Close()` | Wrap as `defer func() { _ = f.Close() }()` |
| `errcheck`: ignored error from `os.Setenv` in tests | `if err := os.Setenv(...); err != nil { t.Fatal(err) }` |
| `staticcheck SA1019`: deprecated API | Replace with the recommended replacement; if the replacement is not yet available in our Go version, `//nolint:staticcheck // SA1019: <reason>` |
| `unused`: dead function | Delete it. If it's exported and intended for plugins, add a `//nolint:unused` with a justification |
| `ineffassign`: unused assignment | Delete the assignment |
| `misspell` | Fix the typo |

- [ ] **Step 5: Re-run lint until clean**

Run: `golangci-lint run ./...`

Expected: exit 0, no output.

- [ ] **Step 6: Commit the config (and cleanup, if any)**

If lint produced no findings:

```bash
git add .golangci.yml
git commit -m "ci: add golangci-lint configuration"
git push
```

If lint produced findings and you fixed them, commit both atomically so the config and the code that satisfies it land together:

```bash
git add .golangci.yml .
git status   # sanity check: only .golangci.yml + the files you fixed should be staged
git commit -m "ci: add golangci-lint configuration and clean up findings"
git push
```

(If the cleanup is large — say, more than ~30 lines across more than ~5 files — split it: first commit the fixes as `refactor:`, then the config as `ci:`.)

---

## Task 4: Add CI workflow

Create a single CI workflow that runs build, test (race + cover), `gofmt`, `go vet`, `golangci-lint`, and a GoReleaser snapshot on every PR and on every push to `main` and `release/**`.

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Create `.github/workflows/ci.yml`**

```yaml
name: ci

on:
  pull_request:
  push:
    branches:
      - main
      - "release/**"

permissions:
  contents: read

concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: true

jobs:
  test:
    name: build / test / lint
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - name: Setup Go
        uses: actions/setup-go@v5
        with:
          go-version: "1.25.x"
          cache: true

      - name: Download modules
        run: |
          go mod download
          go mod verify

      - name: gofmt
        run: |
          out=$(gofmt -d -l .)
          if [ -n "$out" ]; then
            echo "::error::gofmt found formatting issues:"
            echo "$out"
            exit 1
          fi

      - name: go vet
        run: go vet ./...

      - name: golangci-lint
        uses: golangci/golangci-lint-action@v6
        with:
          version: v1.62.2

      - name: Test
        run: go test -race -covermode=atomic ./...

      - name: GoReleaser snapshot
        uses: goreleaser/goreleaser-action@v6
        with:
          distribution: goreleaser
          version: "~> v2"
          args: release --snapshot --clean --skip=publish,sign
```

- [ ] **Step 2: Commit the workflow**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add build/test/lint workflow on PR and push"
git push
```

- [ ] **Step 3: Watch CI run and fix what fails**

Open: `https://github.com/Galvill/jitenv/actions?query=branch%3Arelease%2Fv0.1-prep`

Expected: the `ci` workflow runs against the latest commit. The GoReleaser snapshot step will **fail** because `.goreleaser.yaml` does not exist yet — that is expected and fixed in Task 5. Any other failure is a real problem.

If `gofmt`, `go vet`, `golangci-lint`, or `go test` fails: fix the underlying issue locally, commit (`fix: ...` or `test: ...`), push, and re-watch. Repeat until those four steps are green. Leave the GoReleaser snapshot failure alone for now.

---

## Task 5: Add GoReleaser config and validate locally

Write the `.goreleaser.yaml` and run `goreleaser release --snapshot --clean` to confirm artifacts build. Once it passes, the GoReleaser step in CI will also pass.

**Files:**
- Create: `.goreleaser.yaml`
- Modify: `.gitignore`

- [ ] **Step 1: Install `goreleaser` locally**

Pick whichever method works on your machine:

```bash
# Option A: Go install (slow, builds from source)
go install github.com/goreleaser/goreleaser/v2@latest

# Option B: Download release binary
# https://github.com/goreleaser/goreleaser/releases/latest
```

Verify: `goreleaser --version` prints something like `GitVersion: v2.x.x`.

- [ ] **Step 2: Create `.goreleaser.yaml`**

```yaml
version: 2

project_name: jitenv

before:
  hooks:
    - go mod download

builds:
  - id: jitenv
    main: ./cmd/jitenv
    binary: jitenv
    env:
      - CGO_ENABLED=0
    goos:
      - linux
    goarch:
      - amd64
      - arm64
    flags:
      - -trimpath
    ldflags:
      - -s -w
      - -X github.com/gv/jitenv/internal/cli.Version={{.Version}}
      - -X github.com/gv/jitenv/internal/cli.Commit={{.ShortCommit}}
      - -X github.com/gv/jitenv/internal/cli.Date={{.Date}}

archives:
  - id: jitenv
    formats: [tar.gz]
    name_template: "{{ .Binary }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    files:
      - LICENSE
      - README.md
      - src: internal/shell/snippets/bash.sh
        dst: snippets/bash.sh
      - src: internal/shell/snippets/zsh.sh
        dst: snippets/zsh.sh

nfpms:
  - id: jitenv
    package_name: jitenv
    vendor: Galvill
    homepage: https://github.com/Galvill/jitenv
    maintainer: Galvill <nexvill.ai@gmail.com>
    description: |
      jitenv loads environment variables on demand from pluggable sources
      when configured executables are run, keeping secrets out of the parent
      shell.
    license: MIT
    formats:
      - deb
      - rpm
    bindir: /usr/bin
    contents:
      - src: internal/shell/snippets/bash.sh
        dst: /usr/share/jitenv/snippets/bash.sh
      - src: internal/shell/snippets/zsh.sh
        dst: /usr/share/jitenv/snippets/zsh.sh
      - src: LICENSE
        dst: /usr/share/doc/jitenv/LICENSE
      - src: README.md
        dst: /usr/share/doc/jitenv/README.md

checksum:
  name_template: SHA256SUMS

signs:
  - id: cosign-checksum
    cmd: cosign
    artifacts: checksum
    signature: "${artifact}.sig"
    certificate: "${artifact}.pem"
    args:
      - sign-blob
      - "--output-signature=${signature}"
      - "--output-certificate=${certificate}"
      - "${artifact}"
      - "--yes"

snapshot:
  version_template: "0.1.0-snapshot-{{ .ShortCommit }}"

release:
  github:
    owner: Galvill
    name: jitenv
  draft: false
  prerelease: auto
  header: |
    ## jitenv {{ .Tag }}

    Linux artifacts for `amd64` and `arm64`. Verify integrity:

    ```sh
    sha256sum -c SHA256SUMS

    cosign verify-blob \
      --certificate-identity-regexp "^https://github.com/Galvill/jitenv" \
      --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
      --signature SHA256SUMS.sig --certificate SHA256SUMS.pem SHA256SUMS
    ```
```

- [ ] **Step 3: Validate the config syntax**

Run: `goreleaser check`

Expected: `config is valid`. Fix any reported errors before continuing.

- [ ] **Step 4: Add `dist/` to `.gitignore`**

Replace the contents of `.gitignore` (currently `/bin/`, `*.test`, `*.out`) with:

```
/bin/
/dist/
*.test
*.out
```

- [ ] **Step 5: Run a local snapshot build**

Run: `goreleaser release --snapshot --clean --skip=publish,sign`

Expected: command succeeds in under a minute. `dist/` now contains (with `<sha>` replaced by the short commit):

- `jitenv_0.1.0-snapshot-<sha>_linux_amd64.tar.gz`
- `jitenv_0.1.0-snapshot-<sha>_linux_arm64.tar.gz`
- `jitenv_0.1.0-snapshot-<sha>_linux_amd64.deb`
- `jitenv_0.1.0-snapshot-<sha>_linux_arm64.deb`
- `jitenv_0.1.0-snapshot-<sha>_linux_amd64.rpm`
- `jitenv_0.1.0-snapshot-<sha>_linux_arm64.rpm`
- `SHA256SUMS`
- per-arch binaries under `dist/jitenv_linux_amd64_v1/jitenv` and `dist/jitenv_linux_arm64_v8.0/jitenv`

- [ ] **Step 6: Smoke-test one of the snapshot binaries**

Run:

```bash
./dist/jitenv_linux_amd64_v1/jitenv version
```

Expected output (commit and date will vary):

```
jitenv 0.1.0-snapshot-<sha> (commit <sha>, built <iso8601>)
```

This proves the ldflags injection from `.goreleaser.yaml` reaches the binary.

- [ ] **Step 7: Inspect a `.deb` to confirm contents**

Run: `dpkg-deb -c dist/jitenv_*_linux_amd64.deb | head`

Expected: lists `./usr/bin/jitenv`, `./usr/share/jitenv/snippets/bash.sh`, `./usr/share/jitenv/snippets/zsh.sh`, `./usr/share/doc/jitenv/{LICENSE,README.md}`.

- [ ] **Step 8: Commit**

```bash
git add .goreleaser.yaml .gitignore
git commit -m "ci: add GoReleaser config for linux amd64+arm64 (deb/rpm/tar.gz, cosign keyless)"
git push
```

- [ ] **Step 9: Confirm the CI snapshot step now passes**

Watch the workflow run for the new commit. The `GoReleaser snapshot` step should now go green. If it fails on the runner but passed locally, the most likely cause is a missing tool. The `goreleaser-action` includes the `goreleaser` binary itself, but cosign is *not* needed because we passed `--skip=sign`. If failure persists, paste the action log and inspect.

---

## Task 6: Add release-please config and workflow

Wire up release-please so every push to `main` either opens or updates a "release vX.Y.Z" PR, and merging that PR creates the matching `vX.Y.Z` tag.

**Files:**
- Create: `release-please-config.json`
- Create: `release-please-manifest.json`
- Create: `.github/workflows/release-please.yml`

- [ ] **Step 1: Create `release-please-manifest.json`**

```json
{
  ".": "0.1.0"
}
```

This seeds the next release as `v0.1.0`.

- [ ] **Step 2: Create `release-please-config.json`**

```json
{
  "$schema": "https://raw.githubusercontent.com/googleapis/release-please/main/schemas/config.json",
  "release-type": "go",
  "bump-minor-pre-major": true,
  "include-v-in-tag": true,
  "include-component-in-tag": false,
  "packages": {
    ".": {
      "package-name": "jitenv"
    }
  },
  "changelog-sections": [
    { "type": "feat",     "section": "Features" },
    { "type": "fix",      "section": "Bug Fixes" },
    { "type": "perf",     "section": "Performance Improvements" },
    { "type": "revert",   "section": "Reverts" },
    { "type": "docs",     "section": "Documentation" },
    { "type": "chore",    "hidden": true },
    { "type": "style",    "hidden": true },
    { "type": "refactor", "hidden": true },
    { "type": "test",     "hidden": true },
    { "type": "build",    "hidden": true },
    { "type": "ci",       "hidden": true }
  ]
}
```

- [ ] **Step 3: Create `.github/workflows/release-please.yml`**

```yaml
name: release-please

on:
  push:
    branches:
      - main

permissions:
  contents: write
  pull-requests: write

jobs:
  release-please:
    runs-on: ubuntu-latest
    steps:
      - uses: googleapis/release-please-action@v4
        with:
          config-file: release-please-config.json
          manifest-file: release-please-manifest.json
```

- [ ] **Step 4: Commit**

```bash
git add release-please-config.json release-please-manifest.json .github/workflows/release-please.yml
git commit -m "ci: add release-please for conventional-commit version bumps"
git push
```

- [ ] **Step 5: Verify workflow registers**

Open: `https://github.com/Galvill/jitenv/actions/workflows/release-please.yml`

Expected: GitHub displays the workflow but it has not run yet (it triggers on `push` to `main`, and we are on `release/v0.1-prep`). That is correct behaviour for now.

---

## Task 7: Add tag-triggered release workflow

This is the workflow that actually publishes a Release. It runs only when a `v*` tag is pushed (which release-please does automatically when its release PR is merged).

**Files:**
- Create: `.github/workflows/release.yml`

- [ ] **Step 1: Create `.github/workflows/release.yml`**

```yaml
name: release

on:
  push:
    tags:
      - "v*"

permissions:
  contents: write
  id-token: write

jobs:
  goreleaser:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - name: Setup Go
        uses: actions/setup-go@v5
        with:
          go-version: "1.25.x"
          cache: true

      - name: Install cosign
        uses: sigstore/cosign-installer@v3

      - name: Run GoReleaser
        uses: goreleaser/goreleaser-action@v6
        with:
          distribution: goreleaser
          version: "~> v2"
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "ci: add tag-triggered GoReleaser release workflow"
git push
```

- [ ] **Step 3: Verify workflow registers**

Open: `https://github.com/Galvill/jitenv/actions/workflows/release.yml`

Expected: workflow is listed but has not run (no `v*` tag exists yet). Correct.

---

## Task 8: Open the PR to `main` and merge

This is the integration step. Once `release/v0.1-prep` lands on `main`, release-please will see the merged commits and (on its next scheduled or push-triggered run on main) open the very first release PR.

**Files:** none modified in this task.

- [ ] **Step 1: Confirm the branch is fully green**

Open: `https://github.com/Galvill/jitenv/actions?query=branch%3Arelease%2Fv0.1-prep`

Expected: latest commit's `ci` run is green across all steps. If not, fix locally, push, repeat. Do not open the PR until CI is green.

- [ ] **Step 2: Open the PR**

Run:

```bash
gh pr create --title "ci: first release pipeline (Linux amd64+arm64)" --body "$(cat <<'EOF'
## Summary
- Adds CI workflow (build/test/lint/snapshot) on every PR and push to main.
- Adds GoReleaser config producing `tar.gz`/`.deb`/`.rpm` for `linux/amd64` and `linux/arm64`, with cosign keyless signing of `SHA256SUMS`.
- Adds release-please for Conventional-Commits-driven version bumps.
- Adds tag-triggered release workflow that publishes the GitHub Release.
- Refactors `internal/cli.Version` into ldflags-injectable `Version`/`Commit`/`Date` vars.
- Adds `golangci-lint` config and cleanup.

Spec: `docs/superpowers/specs/2026-05-06-first-release-design.md`.
Plan: `docs/superpowers/plans/2026-05-06-first-release-pipeline.md`.

## Test plan
- [ ] CI workflow green on this PR
- [ ] After merge, release-please opens a "release v0.1.0" PR
- [ ] Merging the release PR tags `v0.1.0` and triggers the release workflow
- [ ] The release workflow produces a GitHub Release with `tar.gz`/`.deb`/`.rpm` for both arches plus `SHA256SUMS`, `SHA256SUMS.sig`, `SHA256SUMS.pem`
- [ ] `cosign verify-blob` succeeds against the published `SHA256SUMS`
EOF
)"
```

- [ ] **Step 3: Merge the PR once CI is green and review approves**

Use the GitHub UI or:

```bash
gh pr merge --squash --delete-branch
```

(Squash so `main`'s history stays clean; release-please reads the squash-merge commit message, which by default is the PR title — `ci:` is hidden from the CHANGELOG, which is what we want for this infra PR.)

---

## Task 9: Cut the first release (`v0.1.0`)

After Task 8 merges, release-please will open its first "release v0.1.0" PR within a minute or two. Merging that PR cuts the actual release.

**Files:** none modified in this task. Verification only.

- [ ] **Step 1: Wait for release-please to open the release PR**

Open: `https://github.com/Galvill/jitenv/pulls?q=is%3Apr+is%3Aopen+author%3Aapp%2Fgithub-actions`

Expected within ~2 minutes of the merge to `main`: a PR titled something like `chore(main): release 0.1.0`, authored by the github-actions bot. The PR diff updates `release-please-manifest.json` to `0.1.0`, creates `CHANGELOG.md`, and records the included commits.

If after 5 minutes there is no PR:

- Check the `release-please` workflow's run for failures: `https://github.com/Galvill/jitenv/actions/workflows/release-please.yml`.
- If the run says it has nothing to release, that means none of the commits in this branch used a *visible* Conventional-Commits type (`feat`, `fix`, `perf`, `revert`, `docs`). All the infra commits are `ci:` / `build:` / `refactor:` — hidden. To force the first release, add a single `feat:` commit to `main` (e.g., `feat: initial v0.1.0 release pipeline`) by editing `CHANGELOG.md`'s placeholder header or `README.md` and pushing through a small follow-up PR. Then release-please will open the release PR.

- [ ] **Step 2: Review the release PR's CHANGELOG**

Open the release PR. Expected:

- `CHANGELOG.md` is created with section headers for any `feat`/`fix`/`perf`/`revert`/`docs` commits since the manifest was seeded.
- `release-please-manifest.json` updated from `0.1.0` to whatever release-please calculated. (If the only visible commit was a `feat:`, this stays at `0.1.0` because it is the seeded version; subsequent releases will bump.)

- [ ] **Step 3: Merge the release PR**

```bash
gh pr merge <release-pr-number> --squash
```

This pushes a tag `v0.1.0` (or `v0.X.Y` per release-please's calculation).

- [ ] **Step 4: Watch the `release` workflow run**

Open: `https://github.com/Galvill/jitenv/actions/workflows/release.yml`

Expected: the workflow runs to completion in roughly 3-5 minutes and produces a GitHub Release at `https://github.com/Galvill/jitenv/releases/tag/v0.1.0` (or the calculated tag).

If the workflow fails:

- `goreleaser` errors usually point to the offending YAML key — fix in a follow-up PR (`fix(ci):` so it shows in the next CHANGELOG, or `ci:` if you want it hidden).
- cosign signing errors usually mean the workflow does not have `id-token: write`. Re-check `permissions:` in `release.yml`.

- [ ] **Step 5: Verify the released artifacts**

Run:

```bash
TAG=v0.1.0
mkdir -p /tmp/jitenv-verify && cd /tmp/jitenv-verify
gh release download "$TAG" --repo Galvill/jitenv
sha256sum -c SHA256SUMS

cosign verify-blob \
  --certificate-identity-regexp "^https://github.com/Galvill/jitenv" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --signature SHA256SUMS.sig --certificate SHA256SUMS.pem SHA256SUMS
```

Expected:

- All checksum lines say `OK`.
- `cosign verify-blob` prints `Verified OK`.

- [ ] **Step 6: Smoke-test one of the released `.deb`s in a throwaway container**

Run:

```bash
docker run --rm -v /tmp/jitenv-verify:/v -it ubuntu:24.04 bash -c '
  apt-get update -qq && apt-get install -y -qq /v/jitenv_*_linux_amd64.deb >/dev/null
  jitenv version
'
```

Expected output (version will match the released tag):

```
jitenv 0.1.0 (commit <sha>, built <iso8601>)
```

This confirms the `.deb` installs cleanly and the binary reports the right version on a vanilla Ubuntu image.

---

## Self-review notes

- **Spec coverage:** §2 scope → Tasks 1–9. §3 versioning → Task 1. §4 repo additions → Tasks 1, 3, 4, 5, 6, 7. §5 CI → Task 4. §6 lint → Task 3. §7 release-please → Task 6. §8 GoReleaser → Task 5. §9 release workflow → Task 7. §10 verification block → embedded in `.goreleaser.yaml` `release.header` (Task 5) and verified in Task 9. §11 build sequence → Tasks 1–9 in this order. §12 risks: lint cleanup (Task 3 step 6 size note), e2e on runners (Task 4 step 3 fix-not-skip directive), release-please token (Task 9 step 1 fallback note), cosign reachability (out-of-scope, documented in spec).
- **No placeholders.** Every code block is complete and exact. Every command has expected output described.
- **Type / signature consistency.** `formatVersion(version, commit, date string)` defined in Task 1 step 4; called identically in step 4. The three vars `Version`, `Commit`, `Date` are referenced by the same names everywhere (Makefile, `.goreleaser.yaml`, root.go).
