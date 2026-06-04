#!/usr/bin/env bash
# Render the Homebrew cask formula for a release tag and push it to
# Galvill/homebrew-jitenv. Replaces the goreleaser homebrew_casks
# pipe in the async release flow (#226) — once notarization is
# Accepted and the tarballs are on the GH release, this is the only
# step left between "notarized binary" and "brew install jitenv".
#
# Usage: cask-publish.sh
#
# Env:
#   TAG                          release tag, e.g. v0.11.0
#   DARWIN_AMD64_TARBALL         path to jitenv_<v>_darwin_amd64.tar.gz
#   DARWIN_ARM64_TARBALL         path to jitenv_<v>_darwin_arm64.tar.gz
#   HOMEBREW_TAP_GITHUB_TOKEN    PAT with write access to homebrew-jitenv
#   CASK_REPO   default Galvill/homebrew-jitenv
#   CASK_NAME   default jitenv
#
# The cask URL template matches what the old goreleaser config emitted
# (see #220) so the user-facing download path doesn't change.

set -euo pipefail

: "${TAG:?TAG required (e.g. v0.11.0)}"
: "${DARWIN_AMD64_TARBALL:?DARWIN_AMD64_TARBALL required}"
: "${DARWIN_ARM64_TARBALL:?DARWIN_ARM64_TARBALL required}"
: "${HOMEBREW_TAP_GITHUB_TOKEN:?HOMEBREW_TAP_GITHUB_TOKEN required}"

CASK_REPO="${CASK_REPO:-Galvill/homebrew-jitenv}"
CASK_NAME="${CASK_NAME:-jitenv}"
VERSION="${TAG#v}"

if [ ! -f "$DARWIN_AMD64_TARBALL" ] || [ ! -f "$DARWIN_ARM64_TARBALL" ]; then
  echo "cask-publish: tarballs not found" >&2
  echo "  amd64: $DARWIN_AMD64_TARBALL" >&2
  echo "  arm64: $DARWIN_ARM64_TARBALL" >&2
  exit 1
fi

amd_sha="$(sha256sum "$DARWIN_AMD64_TARBALL" | awk '{print $1}')"
arm_sha="$(sha256sum "$DARWIN_ARM64_TARBALL" | awk '{print $1}')"
amd_name="$(basename "$DARWIN_AMD64_TARBALL")"
arm_name="$(basename "$DARWIN_ARM64_TARBALL")"

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

# The token is the only auth — use the x-access-token URL form so it
# doesn't have to live in the on-disk git config.
clone_url="https://x-access-token:${HOMEBREW_TAP_GITHUB_TOKEN}@github.com/${CASK_REPO}.git"
git clone --depth 1 "$clone_url" "$workdir/tap"

cd "$workdir/tap"
mkdir -p Casks

# Heredoc renders the cask Ruby with the per-arch URL + sha256 wired
# in. Matches the historical goreleaser-generated layout closely
# enough that downstream users with an existing tap don't see a churn
# diff beyond the version/sha256 lines.
cat > "Casks/${CASK_NAME}.rb" <<EOF
cask "${CASK_NAME}" do
  version "${VERSION}"

  on_arm do
    url "https://github.com/Galvill/jitenv/releases/download/${TAG}/${arm_name}"
    sha256 "${arm_sha}"
  end
  on_intel do
    url "https://github.com/Galvill/jitenv/releases/download/${TAG}/${amd_name}"
    sha256 "${amd_sha}"
  end

  name "jitenv"
  desc "Just-in-time env-var injection from pluggable secret sources, scoped to per-command process trees"
  homepage "https://github.com/Galvill/jitenv"

  binary "jitenv"
  binary "jitenv-hook"
  binary "jitenv-tui"
end
EOF

git config user.name "goreleaserbot"
git config user.email "bot@goreleaser.com"
git add "Casks/${CASK_NAME}.rb"

# No-op when the cask is byte-identical to what's already on the
# branch (e.g. a re-run of finalize against the same tag).
if git diff --cached --quiet; then
  echo "cask-publish: cask unchanged for $TAG — nothing to push"
  exit 0
fi

git commit -m "Brew cask update for ${CASK_NAME} ${TAG}"
git push origin HEAD
echo "cask-publish: pushed ${CASK_NAME} ${TAG} to ${CASK_REPO}"
