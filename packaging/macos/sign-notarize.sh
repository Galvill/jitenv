#!/usr/bin/env bash
# Sign + notarize a single darwin binary with Apple's own toolchain
# (codesign + xcrun notarytool), invoked from each build's
# `hooks.post` in .goreleaser.yaml (#13).
#
# We do NOT use goreleaser's built-in `notarize:` block: its bundled
# `quill` signer verifies the Developer ID cert chain with Go's
# crypto/x509, which rejects Apple's certs ("x509: unhandled critical
# extension"). Apple's native codesign/notarytool have no such issue.
#
# Usage: sign-notarize.sh <binary-path> <goos>
#
# No-ops (exit 0) when:
#   - the target isn't darwin, or
#   - MACOS_SIGN_IDENTITY is unset (signing not configured — e.g. a
#     local `goreleaser release --snapshot` or a fork without secrets).
#
# Required env when signing IS configured (exported by the release
# workflow's keychain-setup step):
#   MACOS_SIGN_IDENTITY     codesigning identity (SHA-1 hash or CN)
#   MACOS_KEYCHAIN          path to the temp keychain holding the cert
#   MACOS_KEYCHAIN_PW       password to unlock that keychain
#   MACOS_NOTARY_KEY_FILE   path to the App Store Connect API key (.p8)
#   MACOS_NOTARY_KEY_ID     the API key ID
#   MACOS_NOTARY_ISSUER_ID  the issuer UUID
#
# Optional:
#   MACOS_NOTARY_TIMEOUT    notarytool --timeout value (e.g. "60m",
#                           "3h"). Default "120m". Wired to the repo
#                           variable of the same name by
#                           macos-release.yml so the cap can be
#                           bumped per-run without a code change.
#
# Bare CLI binaries (Mach-O, not .app/.pkg/.dmg) cannot be stapled, so
# we don't staple — Gatekeeper verifies the notarization online on
# first run. That's why the Homebrew cask no longer strips the
# quarantine xattr.

set -euo pipefail

BIN="${1:?usage: sign-notarize.sh <binary-path> <goos>}"
GOOS="${2:?usage: sign-notarize.sh <binary-path> <goos>}"

if [ "$GOOS" != "darwin" ]; then
  exit 0
fi

if [ -z "${MACOS_SIGN_IDENTITY:-}" ]; then
  echo "sign-notarize: MACOS_SIGN_IDENTITY unset — skipping $BIN (unsigned build)"
  exit 0
fi

: "${MACOS_KEYCHAIN:?MACOS_KEYCHAIN must be set when MACOS_SIGN_IDENTITY is}"
: "${MACOS_NOTARY_KEY_FILE:?MACOS_NOTARY_KEY_FILE must be set}"
: "${MACOS_NOTARY_KEY_ID:?MACOS_NOTARY_KEY_ID must be set}"
: "${MACOS_NOTARY_ISSUER_ID:?MACOS_NOTARY_ISSUER_ID must be set}"

echo "sign-notarize: codesigning $BIN"
# Defensive unlock: GitHub runners can re-lock the keychain between
# steps. Harmless if it's already unlocked.
if [ -n "${MACOS_KEYCHAIN_PW:-}" ]; then
  security unlock-keychain -p "$MACOS_KEYCHAIN_PW" "$MACOS_KEYCHAIN" >/dev/null 2>&1 || true
fi

# --options runtime (hardened runtime) and --timestamp are both
# prerequisites for notarization; --force re-signs if needed.
codesign --force --timestamp --options runtime \
  --keychain "$MACOS_KEYCHAIN" \
  --sign "$MACOS_SIGN_IDENTITY" \
  "$BIN"
codesign --verify --strict --verbose=2 "$BIN"

echo "sign-notarize: notarizing $BIN"
workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT
zip="$workdir/$(basename "$BIN").zip"
# notarytool needs a zip/pkg/dmg container; ditto produces a zip
# notarytool accepts.
ditto -c -k "$BIN" "$zip"

# --wait blocks until Apple finishes. Notarization time is highly
# variable — the smaller jitenv-tui binary completes in ~25 min while
# the larger jitenv binary has been observed still "In Progress" past
# 45m. The cap is overridable at runtime via the MACOS_NOTARY_TIMEOUT
# env var (set as a repo variable on macos-release.yml — see the
# workflow), defaulting to 120m so it just works without configuration.
# Bumping the var lets you retry a release with a longer wait without
# a code change. Goreleaser runs the hooks with bounded concurrency,
# so worst-case wall-clock is ~2 rounds × this value; pick something
# inside GitHub's default 6h job timeout. Since macos-release.yml is
# decoupled from the main release (#212), a long mac run no longer
# blocks lin/win/choco. A real timeout here fails the cask publish
# loudly rather than shipping an un-notarized binary.
notary_timeout="${MACOS_NOTARY_TIMEOUT:-120m}"
echo "sign-notarize: notarytool --timeout $notary_timeout"
xcrun notarytool submit "$zip" \
  --key "$MACOS_NOTARY_KEY_FILE" \
  --key-id "$MACOS_NOTARY_KEY_ID" \
  --issuer "$MACOS_NOTARY_ISSUER_ID" \
  --wait \
  --timeout "$notary_timeout"

echo "sign-notarize: done $BIN"
