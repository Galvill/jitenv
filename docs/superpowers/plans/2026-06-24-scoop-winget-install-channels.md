# Scoop + winget Install Channels Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Scoop and winget-pkgs as Windows install channels, published automatically on each stable release via GoReleaser native pipes.

**Architecture:** Add `scoops:` and `winget:` blocks to the existing `.goreleaser.yaml`, run by the existing `release.yml` job on `ubuntu-latest`. The Scoop pipe commits `bucket/jitenv.json` to `Galvill/scoop-jitenv`; the winget pipe pushes manifests to the `Galvill/winget-pkgs` fork and auto-opens a PR to `microsoft/winget-pkgs`. Both consume the Windows `.zip` archives + `SHA256SUMS` produced earlier in the same run. Chocolatey is unchanged (it stays a separate Windows workflow because `choco` is Windows-only).

**Tech Stack:** GoReleaser v2 (`~> v2`, action v6), GitHub Actions, Scoop, winget.

## Global Constraints

- GoReleaser config schema version: `version: 2` (validated with `goreleaser check`). Local/CI toolchain is GoReleaser **v2.15.4**.
- Cross-repo pushes require the **`RELEASE_PAT`** repo secret (`contents:write` + `pull-requests:write` on `Galvill/scoop-jitenv` and `Galvill/winget-pkgs`). The default `GITHUB_TOKEN` cannot push cross-repo. The secret is already configured.
- Both pipes use `skip_upload: "auto"` so prereleases (`-rc.N` tags) and snapshots never push — matching Chocolatey's RC-skipping.
- `dist/` is gitignored; never commit it.
- Package coordinates (fixed, copied verbatim): Scoop bucket `Galvill/scoop-jitenv`; winget `package_identifier: Galvill.jitenv`, `publisher: Galvill`; homepage `https://github.com/Galvill/jitenv`; license `MIT`; one-line summary `Just-in-time environment variable loader, scoped to per-command process trees`; copyright `© 2026 Gal Villaret`.
- `ci.yml` runs `goreleaser release --snapshot --clean --skip=publish,sign` — it skips the publish stage, so the new pipes don't run there and `ci.yml` needs **no** change and no secret.

## File Structure

- **Modify** `.goreleaser.yaml` — add `scoops:` and `winget:` blocks plus explanatory comments (insert after the Chocolatey comment block, before `checksum:`); extend the `release.header` Windows-install snippet.
- **Modify** `.github/workflows/release.yml` — add `RELEASE_PAT` to the GoReleaser step `env:`.
- **Modify** `README.md` — add `### Scoop (Windows)` and `### winget (Windows)` sections.
- **Modify** `web/quickstart.md` and `web/index.html` — add Scoop + winget install snippets beside Chocolatey.

Verification for the config tasks is two real commands (no unit-test framework applies to YAML config):
1. `goreleaser check` — schema validity.
2. `RELEASE_PAT=dummy goreleaser release --snapshot --clean --skip=sign` — generates the manifests under `dist/` (snapshot auto-skips the push); assert the expected files/content exist.

---

### Task 1: Scoop pipe + RELEASE_PAT wiring

**Files:**
- Modify: `.goreleaser.yaml` (insert `scoops:` block after the Chocolatey comment block that ends `...packaging/chocolatey/ for the details.`, before `checksum:`)
- Modify: `.github/workflows/release.yml` (the `Run GoReleaser` step `env:`)

**Interfaces:**
- Consumes: the existing `archives:` entry with `id: jitenv` (produces the Windows `.zip` carrying `jitenv.exe`, `jitenv-hook.exe`, `jitenv-tui.exe`).
- Produces: `dist/scoop/bucket/jitenv.json` at release time, pushed to `Galvill/scoop-jitenv` on `bucket/jitenv.json`.

- [ ] **Step 1: Add the `scoops:` block to `.goreleaser.yaml`**

Insert immediately after the Chocolatey explanatory comment block (the paragraph ending `See that workflow and packaging/chocolatey/ for the details.`) and the blank line that follows it, directly before `checksum:`:

```yaml
# Scoop (#TODO-issue) and winget (below) ARE goreleaser pipes — unlike
# Chocolatey. Their "publish" step is just committing a manifest file
# (Scoop -> the Galvill/scoop-jitenv bucket) or opening a PR (winget ->
# microsoft/winget-pkgs), pure git work that runs fine on this ubuntu
# release runner. They consume the windows .zip + SHA256SUMS produced
# above in this same run, so there's no async ordering problem like the
# darwin cask. Cross-repo pushes need the RELEASE_PAT secret (the default
# GITHUB_TOKEN can't push to other repos); release.yml passes it through.
# skip_upload: "auto" skips prereleases/snapshots, matching Chocolatey.
scoops:
  - repository:
      owner: Galvill
      name: scoop-jitenv
      branch: main
      token: "{{ .Env.RELEASE_PAT }}"
    directory: bucket
    homepage: "https://github.com/Galvill/jitenv"
    description: "Just-in-time environment variable loader, scoped to per-command process trees"
    license: MIT
    skip_upload: "auto"
```

- [ ] **Step 2: Add `RELEASE_PAT` to the release workflow**

In `.github/workflows/release.yml`, the `Run GoReleaser` step currently ends with:

```yaml
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

Change it to:

```yaml
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          # Cross-repo pushes for the Scoop bucket + winget fork; the
          # default GITHUB_TOKEN can't push to other repositories.
          RELEASE_PAT: ${{ secrets.RELEASE_PAT }}
```

- [ ] **Step 3: Validate config schema**

Run: `goreleaser check`
Expected: `1 configuration file(s) validated` and no errors.

- [ ] **Step 4: Generate the Scoop manifest via snapshot**

Run: `RELEASE_PAT=dummy goreleaser release --snapshot --clean --skip=sign`
Expected: log line `scoop manifests ... writing manifest=dist/scoop/bucket/jitenv.json`, and the build completes (`thanks for using GoReleaser!`). Nothing is pushed (snapshot auto-skips upload).

- [ ] **Step 5: Assert the Scoop manifest content**

Run: `cat dist/scoop/bucket/jitenv.json`
Expected: JSON with `architecture.64bit` and `architecture.arm64`, each with a `url` under `releases/download/`, a 64-hex `hash`, and a `bin` array containing `jitenv.exe`, `jitenv-hook.exe`, `jitenv-tui.exe`; plus top-level `homepage`, `license: MIT`, and the description string.

- [ ] **Step 6: Commit**

```bash
git add .goreleaser.yaml .github/workflows/release.yml
git commit -m "feat(release): publish a Scoop manifest via goreleaser

Add a scoops: pipe that commits bucket/jitenv.json to
Galvill/scoop-jitenv on each stable release, and pass RELEASE_PAT
through release.yml for the cross-repo push.

Claude-Session: https://claude.ai/code/session_01LZ4n128Nc4CU4oLBx59Vpr"
```

---

### Task 2: winget pipe

**Files:**
- Modify: `.goreleaser.yaml` (add `winget:` block immediately after the `scoops:` block from Task 1)

**Interfaces:**
- Consumes: the same `archives:` entry `id: jitenv` Windows `.zip` and the `RELEASE_PAT` wiring added in Task 1.
- Produces: `dist/winget/manifests/g/Galvill/jitenv/<version>/Galvill.jitenv{,.installer,.locale.en-US}.yaml`, pushed to a branch on `Galvill/winget-pkgs` with an auto-opened PR to `microsoft/winget-pkgs` `master`.

- [ ] **Step 1: Add the `winget:` block to `.goreleaser.yaml`**

Insert directly after the `scoops:` block (still before `checksum:`):

```yaml
winget:
  - name: jitenv
    publisher: Galvill
    publisher_url: "https://github.com/Galvill"
    author: Gal Villaret
    short_description: "Just-in-time environment variable loader, scoped to per-command process trees"
    description: |
      jitenv loads environment variables on demand from pluggable sources
      when configured executables are run, keeping secrets out of the parent
      shell. Linux, macOS, and Windows.
    license: MIT
    license_url: "https://github.com/Galvill/jitenv/blob/main/LICENSE"
    copyright: "© 2026 Gal Villaret"
    homepage: "https://github.com/Galvill/jitenv"
    release_notes_url: "https://github.com/Galvill/jitenv/releases/tag/{{ .Tag }}"
    tags:
      - secrets
      - cli
      - environment-variables
      - shell-hook
    package_identifier: Galvill.jitenv
    repository:
      owner: Galvill
      name: winget-pkgs
      branch: "jitenv-{{ .Version }}"
      token: "{{ .Env.RELEASE_PAT }}"
      pull_request:
        enabled: true
        base:
          owner: microsoft
          name: winget-pkgs
          branch: master
    skip_upload: "auto"
```

- [ ] **Step 2: Validate config schema**

Run: `goreleaser check`
Expected: `1 configuration file(s) validated` and no errors.

- [ ] **Step 3: Generate the winget manifests via snapshot**

Run: `RELEASE_PAT=dummy goreleaser release --snapshot --clean --skip=sign`
Expected: `winget ... writing path=dist/winget/manifests/g/Galvill/jitenv/<version>/Galvill.jitenv.yaml` (plus `.installer.yaml` and `.locale.en-US.yaml`), build completes, nothing pushed.

- [ ] **Step 4: Assert the winget manifest content**

Run: `cat dist/winget/manifests/g/Galvill/jitenv/*/Galvill.jitenv.installer.yaml`
Expected: `PackageIdentifier: Galvill.jitenv`, `InstallerType: zip`, two `Installers` entries (`Architecture: x64` and `Architecture: arm64`), each with `NestedInstallerType: portable` and `NestedInstallerFiles` listing `jitenv.exe`/`jitenv-hook.exe`/`jitenv-tui.exe` with `PortableCommandAlias` values, a `releases/download/` `InstallerUrl`, and a 64-hex `InstallerSha256`.

Run: `cat dist/winget/manifests/g/Galvill/jitenv/*/Galvill.jitenv.locale.en-US.yaml`
Expected: includes `Publisher: Galvill`, `PublisherUrl`, `Author: Gal Villaret`, `License: MIT`, `LicenseUrl`, `Copyright`, `Moniker: jitenv`, `Tags`, and the short description.

- [ ] **Step 5: Commit**

```bash
git add .goreleaser.yaml
git commit -m "feat(release): open a winget-pkgs PR via goreleaser

Add a winget: pipe that pushes Galvill.jitenv manifests to the
Galvill/winget-pkgs fork and auto-opens a PR to microsoft/winget-pkgs
on each stable release.

Claude-Session: https://claude.ai/code/session_01LZ4n128Nc4CU4oLBx59Vpr"
```

---

### Task 3: Documentation (README, release header, website)

**Files:**
- Modify: `README.md` (after the `### Chocolatey (Windows)` section, ends with "...PowerShell 7+ is required for the hook.")
- Modify: `.goreleaser.yaml` (`release.header`, the Windows-install snippet)
- Modify: `web/quickstart.md` (after the `### Windows — Chocolatey` block)
- Modify: `web/index.html` (after the `<h3>Windows &mdash; Chocolatey</h3>` block)

**Interfaces:**
- Consumes: nothing in code — documents the channels shipped by Tasks 1 and 2.
- Produces: user-facing install instructions for Scoop and winget.

- [ ] **Step 1: Add README sections**

In `README.md`, immediately after the Chocolatey section (which ends with the line `or right-click → Properties → Unblock, clears it. PowerShell 7+ is` / `required for the hook.`), insert:

```markdown
### Scoop (Windows)

```powershell
scoop bucket add jitenv https://github.com/Galvill/scoop-jitenv
scoop install jitenv
```

Adds a dedicated [Scoop](https://scoop.sh) bucket and installs from it.
Scoop shims `jitenv.exe`, `jitenv-hook.exe`, and `jitenv-tui.exe` onto
`%PATH%`; `scoop update jitenv` picks up new releases. After install,
activate the PowerShell hook **once**:

```powershell
jitenv hook install
# open a new pwsh tab — the hook is live
```

### winget (Windows)

```powershell
winget install Galvill.jitenv
```

Published to the [winget-pkgs](https://github.com/microsoft/winget-pkgs)
community repository as `Galvill.jitenv`. `winget upgrade Galvill.jitenv`
picks up new releases. After install, activate the PowerShell hook
**once**:

```powershell
jitenv hook install
# open a new pwsh tab — the hook is live
```

As with Chocolatey, the first run of `jitenv.exe` may trip SmartScreen
because the binary is not yet Authenticode-signed (tracked in
[#39](https://github.com/Galvill/jitenv/issues/39)) — `Unblock-File`,
or right-click → Properties → Unblock, clears it. PowerShell 7+ is
required for the hook.
```

- [ ] **Step 2: Extend the GoReleaser release header**

In `.goreleaser.yaml`, the `release.header` currently contains:

```yaml
    Windows users can install via Chocolatey:

    ```powershell
    choco install jitenv
    ```
```

Replace that with:

```yaml
    Windows users can install via Chocolatey, Scoop, or winget:

    ```powershell
    choco install jitenv
    # or
    scoop bucket add jitenv https://github.com/Galvill/scoop-jitenv; scoop install jitenv
    # or
    winget install Galvill.jitenv
    ```
```

- [ ] **Step 3: Add website (quickstart.md) snippets**

In `web/quickstart.md`, after the `### Windows — Chocolatey` block (which ends `PowerShell 7+ is required for the hook.`), insert:

```markdown
### Windows — Scoop

```powershell
scoop bucket add jitenv https://github.com/Galvill/scoop-jitenv
scoop install jitenv
```

Adds a dedicated Scoop bucket and shims `jitenv.exe`, `jitenv-hook.exe`,
and `jitenv-tui.exe` onto `%PATH%`. PowerShell 7+ is required for the hook.

### Windows — winget

```powershell
winget install Galvill.jitenv
```

Installs `Galvill.jitenv` from the winget-pkgs community repository.
PowerShell 7+ is required for the hook.
```

- [ ] **Step 4: Add website (index.html) snippets**

In `web/index.html`, after the Chocolatey block (the `<p>...</p>` that ends `is required for the hook.</p>` following `<h3>Windows &mdash; Chocolatey</h3>`), insert:

```html
    <h3>Windows &mdash; Scoop</h3>
    <pre><code>scoop bucket add jitenv https://github.com/Galvill/scoop-jitenv
scoop install jitenv</code></pre>
    <p>
      Adds a dedicated Scoop bucket and shims <code>jitenv.exe</code>,
      <code>jitenv-hook.exe</code>, and <code>jitenv-tui.exe</code> onto
      <code>%PATH%</code>. PowerShell&nbsp;7+ is required for the hook.
    </p>

    <h3>Windows &mdash; winget</h3>
    <pre><code>winget install Galvill.jitenv</code></pre>
    <p>
      Installs <code>Galvill.jitenv</code> from the winget-pkgs community
      repository. PowerShell&nbsp;7+ is required for the hook.
    </p>
```

- [ ] **Step 5: Validate config still parses**

Run: `goreleaser check`
Expected: `1 configuration file(s) validated`.

- [ ] **Step 6: Commit**

```bash
git add README.md .goreleaser.yaml web/quickstart.md web/index.html
git commit -m "docs: document Scoop + winget Windows install channels

Claude-Session: https://claude.ai/code/session_01LZ4n128Nc4CU4oLBx59Vpr"
```

---

## Post-merge manual verification (not automated)

After this merges and the next **stable** (non-RC) release publishes:

1. Confirm `Galvill/scoop-jitenv` received an updated `bucket/jitenv.json`.
2. Confirm a PR was opened against `microsoft/winget-pkgs` for `Galvill.jitenv` (then shepherd it through Microsoft moderation).
3. On a Windows host: `scoop bucket add jitenv https://github.com/Galvill/scoop-jitenv && scoop install jitenv && jitenv --version`; and once the winget PR merges, `winget install Galvill.jitenv && jitenv --version`.

These require a published release + the PAT + a Windows machine, so they stay manual.

## Notes / risks

- **winget moderation** is outside our control; a rejected/delayed upstream PR doesn't affect the Scoop bucket or the GitHub-release artifacts.
- **Missing/expired `RELEASE_PAT`** will fail the publish step on a *stable* release (intended loud failure). Prereleases/snapshots are unaffected (`skip_upload: "auto"`).
- **`#TODO-issue`** in the `.goreleaser.yaml` comment: replace with the real tracking issue number if one is filed, or drop the parenthetical.
