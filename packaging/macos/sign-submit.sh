#!/usr/bin/env bash
# Sign + SUBMIT a single darwin binary for notarization, without
# waiting for Apple to finish. Replaces sign-notarize.sh in the async
# release flow (#226).
#
# Usage: sign-submit.sh <binary-path> <goos>
#
# Behavior:
#   - codesigns the binary with Apple's hardened runtime + timestamp,
#     identical to the synchronous helper. Verifies the signature.
#   - dittos the binary into a zip and calls `xcrun notarytool submit`
#     WITHOUT --wait. notarytool returns a submission UUID; we capture
#     it and append one record per binary to $NOTARY_SUBMISSIONS_FILE
#     for the polling job to consume:
#         {"id":"<uuid>","name":"jitenv","goarch":"amd64","path":"..."}
#   - exits immediately. Apple's queue might take minutes or hours;
#     that wait happens out-of-band in macos-notarize-poll.yml.
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
#   NOTARY_SUBMISSIONS_FILE path where we append the JSONL manifest.
#                           Created if missing.
#
# Bare CLI Mach-O binaries cannot be stapled, so the bytes that go in
# are exactly the bytes Apple validates — once the submission is
# Accepted, the file on disk is shippable as-is. That's also why
# macos-release-finalize.yml doesn't need a macOS runner: there is no
# stapling step left after Apple says yes.

set -euo pipefail

BIN="${1:?usage: sign-submit.sh <binary-path> <goos>}"
GOOS="${2:?usage: sign-submit.sh <binary-path> <goos>}"

if [ "$GOOS" != "darwin" ]; then
  exit 0
fi

if [ -z "${MACOS_SIGN_IDENTITY:-}" ]; then
  echo "sign-submit: MACOS_SIGN_IDENTITY unset — skipping $BIN (unsigned build)"
  exit 0
fi

: "${MACOS_KEYCHAIN:?MACOS_KEYCHAIN must be set when MACOS_SIGN_IDENTITY is}"
: "${MACOS_NOTARY_KEY_FILE:?MACOS_NOTARY_KEY_FILE must be set}"
: "${MACOS_NOTARY_KEY_ID:?MACOS_NOTARY_KEY_ID must be set}"
: "${MACOS_NOTARY_ISSUER_ID:?MACOS_NOTARY_ISSUER_ID must be set}"
: "${NOTARY_SUBMISSIONS_FILE:?NOTARY_SUBMISSIONS_FILE must be set}"

echo "sign-submit: codesigning $BIN"
# Defensive unlock — GitHub runners can re-lock the keychain between
# steps. Harmless if it's already unlocked.
if [ -n "${MACOS_KEYCHAIN_PW:-}" ]; then
  security unlock-keychain -p "$MACOS_KEYCHAIN_PW" "$MACOS_KEYCHAIN" >/dev/null 2>&1 || true
fi

codesign --force --timestamp --options runtime \
  --keychain "$MACOS_KEYCHAIN" \
  --sign "$MACOS_SIGN_IDENTITY" \
  "$BIN"
codesign --verify --strict --verbose=2 "$BIN"

echo "sign-submit: notarytool submit (no-wait) $BIN"
workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT
zip="$workdir/$(basename "$BIN").zip"
ditto -c -k "$BIN" "$zip"

# `--output-format json` makes the submission-id machine-readable. On
# success notarytool prints something like:
#   {"id":"abc-123","path":"…","message":"Successfully uploaded file"}
# A submit-side failure (network blip, bad key) leaves the file
# untouched and propagates a non-zero exit through `set -e`.
submit_json="$(xcrun notarytool submit "$zip" \
  --key "$MACOS_NOTARY_KEY_FILE" \
  --key-id "$MACOS_NOTARY_KEY_ID" \
  --issuer "$MACOS_NOTARY_ISSUER_ID" \
  --output-format json)"

uuid="$(printf '%s' "$submit_json" | sed -n 's/.*"id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
if [ -z "$uuid" ]; then
  echo "sign-submit: ERROR — couldn't parse submission UUID from notarytool output:" >&2
  echo "$submit_json" >&2
  exit 1
fi

# Derive arch from goreleaser's binary naming: build IDs are
# `jitenv` / `jitenv-tui`, paths are like
# `dist/jitenv_darwin_arm64_v8.0/jitenv` — so the parent dir holds the
# goarch we want. Falls back to "" when the layout is unfamiliar
# (e.g. a manual local run).
parent="$(basename "$(dirname "$BIN")")"
goarch=""
case "$parent" in
  *_darwin_arm64*) goarch="arm64" ;;
  *_darwin_amd64*) goarch="amd64" ;;
esac

# JSONL — one submission per line. Easy to read in Bash without a
# real JSON parser.
printf '{"id":"%s","name":"%s","goarch":"%s","path":"%s"}\n' \
  "$uuid" "$(basename "$BIN")" "$goarch" "$BIN" \
  >> "$NOTARY_SUBMISSIONS_FILE"

echo "sign-submit: queued $BIN (uuid=$uuid arch=$goarch)"
