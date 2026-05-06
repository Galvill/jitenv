# Releasing jitenv

How releases are cut, how to verify them as a user, and how to recover when the automation breaks.

## TL;DR — happy path

1. Land changes on `main` using [Conventional Commits](https://www.conventionalcommits.org). At minimum, one `feat:`, `fix:`, `perf:`, `revert:`, or `docs:` commit must be present since the last release — `chore:`/`ci:`/`build:`/`refactor:`/`style:`/`test:` are CHANGELOG-hidden and won't trigger a release on their own.
2. Within ~2 minutes, `release-please` opens (or updates) a `chore(main): release X.Y.Z` PR.
3. Review the PR's `CHANGELOG.md` diff and merge it.
4. The merge creates tag `vX.Y.Z` and fires `.github/workflows/release.yml`.
5. ~3–5 minutes later, the GitHub Release at `https://github.com/Galvill/jitenv/releases/tag/vX.Y.Z` carries:
   - `jitenv_X.Y.Z_linux_amd64.tar.gz` / `_arm64.tar.gz`
   - `jitenv_X.Y.Z_linux_amd64.deb` / `_arm64.deb`
   - `jitenv_X.Y.Z_linux_amd64.rpm` / `_arm64.rpm`
   - `SHA256SUMS`, `SHA256SUMS.sig`, `SHA256SUMS.pem`

That's it. No manual tagging, no manual artifact builds, no manual signing.

## Versioning rules

`bump-minor-pre-major: true` is set in `release-please-config.json`. While we're pre-1.0:

| Commit type | Version effect |
|---|---|
| `fix:` / `perf:` | patch (`0.1.0` → `0.1.1`) |
| `feat:` | minor (`0.1.0` → `0.2.0`) |
| `feat!:` / `BREAKING CHANGE:` footer | minor while pre-1.0 (the flag's whole purpose) |

Once `v1.0.0` ships, remove `bump-minor-pre-major` from the config so `feat!:` resumes bumping major.

## Verifying a release as a user

```sh
TAG=v0.1.1
mkdir -p /tmp/jitenv-verify && cd /tmp/jitenv-verify
gh release download "$TAG" --repo Galvill/jitenv

# Integrity:
sha256sum -c SHA256SUMS

# Provenance (no key custody — verifies against GH OIDC issuer):
cosign verify-blob \
  --certificate-identity-regexp "^https://github.com/Galvill/jitenv/" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --signature SHA256SUMS.sig --certificate SHA256SUMS.pem SHA256SUMS
```

A successful run prints `OK` for every file and `Verified OK` from cosign. Any mismatch means the artifact is corrupted, tampered, or was not produced by this repo's release workflow.

## Recovery

### Case 1: A `feat:`/`fix:` landed on `main` but no release PR appeared

Most likely the merged commits all used CHANGELOG-hidden types. Check the latest run of `release-please.yml` — if it logged "no releases needed", that's the cause. Land any visible-type commit (a `docs:` is enough) and the next release-please run will pick it up.

If the run failed instead of saying "no releases needed", read the log. The two common failure modes:

- **`HttpError: Resource not accessible by integration`** — the `RELEASE_PLEASE_TOKEN` secret is missing, expired, or has the wrong scopes. Re-issue and re-add it (see "Rotating the PAT" below).
- **YAML or config parse error** — somebody edited `release-please-config.json` or the workflow file. Revert.

### Case 2: A release PR was merged but `release.yml` never fired

This is the v0.1.1 incident. Diagnosis: `gh run list --workflow=release.yml --limit 1` returns empty for the merge. Cause: the tag was created with `GITHUB_TOKEN` instead of the PAT — either the PAT secret was missing at the time, or `release-please.yml` regressed to not pass `token:`.

**Immediate unblock** (publishes artifacts for the existing tag without re-cutting the release):

```sh
git push --delete origin vX.Y.Z   # release goes to draft
git push origin vX.Y.Z            # re-pushes same SHA — fires release.yml
```

The release re-publishes once the workflow finishes uploading assets.

**Long-term fix:** ensure `RELEASE_PLEASE_TOKEN` is set and `release-please.yml` passes `token: ${{ secrets.RELEASE_PLEASE_TOKEN }}` to the action. See `docs/superpowers/specs/2026-05-06-first-release-design.md` §7 for why this matters (GitHub Actions does not fire workflows for tags created by `GITHUB_TOKEN`).

### Case 3: `release.yml` ran but failed mid-build

`gh run view <run-id> --log-failed` to read the failing step. Common causes:

- **GoReleaser fails on dirty/shallow clone** — `actions/checkout@v4` with `fetch-depth: 0` is mandatory; if someone trimmed it, restore it.
- **`cosign sign-blob` fails with no OIDC token** — `permissions: id-token: write` was removed from `release.yml`. Restore it.
- **`go-version: "1.25.x"` not resolved** — bump to the current stable Go minor in both `ci.yml` and `release.yml` together.

After fixing, re-run via `gh workflow run release.yml --ref vX.Y.Z` (the workflow has no `workflow_dispatch:` trigger today; if you need to re-run repeatedly, add it as part of the fix).

### Case 4: Cosign keyless verification fails for a downloaded artifact

If `sha256sum -c` passes but `cosign verify-blob` fails:

- Confirm you used the exact identity regex (`^https://github.com/Galvill/jitenv/` — note the trailing slash).
- Confirm the OIDC issuer (`https://token.actions.githubusercontent.com`) — Sigstore occasionally rotates intermediate certs but the issuer is stable.
- Confirm `cosign` ≥ v2.0 (`cosign version`). v1.x has different verification flags.

If sha256 itself fails, the artifact was tampered or corrupted in transit — re-download and re-verify before trusting.

## Rotating the PAT

The `RELEASE_PLEASE_TOKEN` is a fine-grained PAT (https://github.com/settings/personal-access-tokens) scoped to `Galvill/jitenv` only with:

- **Contents:** read + write
- **Pull requests:** read + write
- **Metadata:** read (auto)

Rotate annually or on any suspicion of compromise:

1. Generate a new token with the same scopes.
2. Update the repo secret at https://github.com/Galvill/jitenv/settings/secrets/actions.
3. Delete the old token from the user's PAT list.
4. No workflow changes needed.

If the token expires before you rotate, `release-please.yml` will fail with `Resource not accessible by integration` on its next run. The next release after rotation works automatically.

## Forcing a release of a specific version

Don't. Trust release-please's calculation. If the calculated version is wrong, the commit history is wrong — fix that with a follow-up commit using a more accurate Conventional Commits prefix (e.g. add a `feat!:` if you genuinely had a breaking change but tagged it `fix:`).

The exception: if release-please somehow misses a commit type (e.g. a malformed footer), you can override the next bump by:

1. Editing `release-please-manifest.json` directly on `main` to set the desired `vX.Y.Z`.
2. Pushing.
3. Letting release-please open a PR against that target version.

This is escape-hatch territory. Document why in the commit message.

## Pre-flight checklist for a release

Before merging the release-please PR, look at the PR description and confirm:

- [ ] CI on `main` is green.
- [ ] The CHANGELOG entries are user-facing language, not internal jargon.
- [ ] Anything new under `feat:` is actually documented somewhere a user would find it (README, `--help`, or a separate doc).
- [ ] If this is a `feat!:` or anyone says "this changes behaviour", the breaking change is called out clearly in the CHANGELOG body (not just in the commit footer).
- [ ] The version bump matches expectations (don't ship `0.2.0` if you only meant a patch).

If any of those fail, fix on `main` *before* merging the release PR — release-please will update the PR to reflect the new state.
