# Scoop + winget install channels — design

**Date:** 2026-06-24
**Status:** Approved (design), pending implementation plan
**Tracking:** adds Scoop and winget-pkgs as Windows install methods alongside the existing Chocolatey channel.

## Goal

Give Windows users two additional, idiomatic install paths beyond Chocolatey:

```powershell
# Scoop
scoop bucket add jitenv https://github.com/Galvill/scoop-jitenv
scoop install jitenv

# winget
winget install Galvill.jitenv
```

Both channels must update automatically on each stable release, with no
manual per-release work beyond the existing release-cut flow.

## Approach

Use **GoReleaser's native `scoops:` and `winget:` pipes** inside the
existing `.goreleaser.yaml`, run by the existing `release.yml` job on
`ubuntu-latest`.

Rationale — why these differ from Chocolatey:

- Chocolatey is a **separate `windows-latest` workflow**
  (`.github/workflows/chocolatey.yml`) only because the `choco` CLI used
  to pack/push the `.nupkg` exists only on Windows.
- Scoop and winget "publishing" is just **generating manifest files and
  committing them / opening a PR** — pure git operations that run fine on
  Linux. GoReleaser's pipes do exactly this.
- Both pipes consume the **Windows `.zip` archives + `SHA256SUMS`** that
  are produced earlier in the *same* GoReleaser run, so there is no
  cross-run ordering problem (unlike the macOS cask, which ships
  asynchronously after notarization).

This keeps all logic that *can* run on the Linux release runner in one
place, and avoids two more standalone workflows.

## Prerequisites (DONE — completed manually by maintainer)

1. **`Galvill/scoop-jitenv`** — new public repo holding the Scoop bucket
   manifest (`bucket/jitenv.json`). GoReleaser commits the manifest here.
2. **`Galvill/winget-pkgs`** — a fork of `microsoft/winget-pkgs`.
   GoReleaser pushes the generated manifest to a branch on this fork, then
   opens a PR to upstream `microsoft/winget-pkgs`.
3. **`RELEASE_PAT` repo secret** — a PAT with `contents:write` +
   `pull-requests:write` on both repos above. Required because the default
   `GITHUB_TOKEN` cannot push to other repositories.

## Components

### 1. `.goreleaser.yaml` — `scoops:` block

- `repository`: `owner: Galvill`, `name: scoop-jitenv`, token from
  `{{ .Env.RELEASE_PAT }}`.
- `directory: bucket` — manifest lands at `bucket/jitenv.json`.
- `homepage: https://github.com/Galvill/jitenv`
- `description`: the one-line summary used elsewhere ("Just-in-time
  environment variable loader, scoped to per-command process trees").
- `license: MIT`
- `commit_author`: bot identity matching the project's other automated
  commits.
- `skip_upload: "auto"` — skips prereleases (the `-rc.N` tags), matching
  Chocolatey's RC-skipping behavior for free.
- Use the existing `jitenv` archive `id` so the pipe picks the Windows
  `.zip` (carries `jitenv.exe`, `jitenv-hook.exe`, `jitenv-tui.exe`).
  Scoop auto-shims the `.exe`s onto PATH so `jitenv config`
  (re-execs `jitenv-tui`) works.

### 2. `.goreleaser.yaml` — `winget:` block

- `name: jitenv`
- `publisher: Galvill`
- `package_identifier: Galvill.jitenv`
- `license: MIT`, `homepage`, `short_description` / `description` reusing
  the existing summary text.
- `repository`: the fork (`owner: Galvill`, `name: winget-pkgs`,
  `branch: <generated>`), token from `{{ .Env.RELEASE_PAT }}`, with
  `pull_request.enabled: true` and `pull_request.base` targeting
  `owner: microsoft`, `name: winget-pkgs`, `branch: master`.
- `skip_upload: "auto"` — skip prereleases.
- winget only supports a single architecture per installer entry but
  handles amd64 + arm64 fine via multiple installer entries, which
  GoReleaser emits from the Windows archives automatically.
- Note for the SmartScreen/unsigned-binary caveat (#39) carried in the
  manifest description, mirroring the Chocolatey package.

### 3. Header comments in `.goreleaser.yaml`

Add comments next to the new blocks explaining *why* Scoop/winget live in
the main pipeline while Chocolatey does not (cross-repo file/PR generation
vs. the Windows-only `choco` CLI), mirroring the existing explanatory
comments for the Homebrew cask and Chocolatey.

### 4. `.github/workflows/release.yml`

Add `RELEASE_PAT: ${{ secrets.RELEASE_PAT }}` to the GoReleaser step's
`env:` (alongside `GITHUB_TOKEN`). No structural change to the job.

### 5. Documentation

- **README.md** — add `### Scoop (Windows)` and `### winget (Windows)`
  sections beside the existing `### Chocolatey (Windows)` section, with
  the install commands and the hook-activation reminder.
- **GoReleaser `release.header`** — extend the Windows install snippet to
  mention `scoop install jitenv` and `winget install Galvill.jitenv`
  alongside `choco install jitenv`.
- **Website / `web/` install docs** — add the two channels wherever
  Chocolatey is documented. Confirm during planning whether
  `website-update.yml`'s version-pin refresh needs to track these (Scoop
  and winget versions are auto-managed by the pipes, so a pin is likely
  unnecessary — verify).

## Error handling / edge cases

- **Missing `RELEASE_PAT`:** with `skip_upload: "auto"` the pipes still
  generate manifests for inspection but the release won't hard-fail on a
  prerelease; on a stable release a missing token *will* fail the push
  step. Since the secret is configured (prereq 3), this is the intended
  loud-failure path. Document the secret requirement near the blocks.
- **Prereleases (`-rc.N`):** skipped via `skip_upload: "auto"`, matching
  Chocolatey. RCs never reach the Scoop bucket or open a winget PR.
- **winget moderation:** Microsoft's validation/moderation may reject or
  delay a PR. This is outside our control; the PR is opened
  automatically and tracked upstream. Failure of the upstream PR does not
  affect the Scoop or GitHub-release artifacts.

## Testing / verification

- **Config validity:** `goreleaser check` (run in CI via `ci.yml` —
  confirm during planning it covers the updated config).
- **Dry run:** `goreleaser release --snapshot --clean --skip=publish`
  locally/CI to confirm both `bucket/jitenv.json` and the winget manifest
  set generate with correct version/URL/SHA256 substitutions.
- **End-to-end:** real install (`scoop install`, `winget install`)
  verified manually on Windows post-merge after the first stable release
  that exercises the pipes. Documented as a manual smoke test — not
  automated (requires a published release + the PAT + a Windows host).

## Out of scope

- Authenticode signing of the Windows binaries (tracked separately, #39).
- Changing the Chocolatey channel.
- Submitting the Scoop manifest to `ScoopInstaller/Extras` (we ship a
  dedicated bucket instead).
