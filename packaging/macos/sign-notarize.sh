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

# --wait blocks until Apple finishes. Notarization time is variable —
# usually a few minutes, but Apple's service routinely exceeds 20m
# during backlogs (a 20m cap timed out a real v0.10.0 release with the
# submission still "In Progress"). 45m comfortably covers slow periods
# while staying well under the GitHub job timeout; the build hooks run
# concurrently so wall-clock is ~the slowest single submission, not the
# sum. A timeout here fails the release loudly rather than shipping an
# un-notarized binary.
xcrun notarytool submit "$zip" \
  --key "$MACOS_NOTARY_KEY_FILE" \
  --key-id "$MACOS_NOTARY_KEY_ID" \
  --issuer "$MACOS_NOTARY_ISSUER_ID" \
  --wait \
  --timeout 45m

echo "sign-notarize: done $BIN"
