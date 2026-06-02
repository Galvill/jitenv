#!/usr/bin/env bash
# Poll the App Store Connect REST API for the status of each
# notarization submission listed in $NOTARY_SUBMISSIONS_FILE. Runs on
# Ubuntu (no `xcrun` dependency) so the polling loop costs ~1000× less
# than burning macos-latest minutes (#226).
#
# Usage: notarize-poll.sh
#
# Env (same set as sign-submit.sh):
#   MACOS_NOTARY_KEY_FILE   path to the App Store Connect .p8 (EC P-256)
#   MACOS_NOTARY_KEY_ID     key id
#   MACOS_NOTARY_ISSUER_ID  issuer uuid
#   NOTARY_SUBMISSIONS_FILE JSONL manifest produced by sign-submit.sh
#                           (one {"id":...,"name":...,"goarch":...} per line)
#   GO                      optional path to `go` (default: from $PATH)
#
# Exit codes drive the polling workflow's decision:
#   0  all submissions Accepted → caller dispatches the finalize job.
#   2  at least one still In Progress → caller exits 0 and lets the
#      next cron tick try again.
#   1  at least one Invalid/Rejected → caller fails the workflow run.
#      The script prints the notarization log for each failure before
#      returning so the maintainer doesn't have to drive xcrun
#      themselves.
#
# Why an exit code rather than a status JSON: keeps the workflow YAML
# simple — `if poll; then dispatch-finalize` for accepted,
# `elif [ $? -eq 2 ]; then exit 0` for still-pending, `else fail`. The
# detail JSON is printed to stdout regardless for the workflow log.

set -euo pipefail

: "${MACOS_NOTARY_KEY_FILE:?MACOS_NOTARY_KEY_FILE must be set}"
: "${MACOS_NOTARY_KEY_ID:?MACOS_NOTARY_KEY_ID must be set}"
: "${MACOS_NOTARY_ISSUER_ID:?MACOS_NOTARY_ISSUER_ID must be set}"
: "${NOTARY_SUBMISSIONS_FILE:?NOTARY_SUBMISSIONS_FILE must be set}"

if [ ! -s "$NOTARY_SUBMISSIONS_FILE" ]; then
  echo "notarize-poll: no submissions in $NOTARY_SUBMISSIONS_FILE" >&2
  exit 1
fi

GO_BIN="${GO:-go}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Build the JWT once per invocation — Apple's TTL is 20 minutes and a
# whole poll cycle finishes in seconds.
JWT="$("$GO_BIN" run "$script_dir/notarize-jwt")"

api="https://api.appstoreconnect.apple.com/notary/v2"
any_pending=0
any_rejected=0

# Read line-by-line so we don't fork awk/jq just for trivial JSONL.
# The manifest is small (≤4 lines for jitenv + jitenv-tui × amd64+arm64).
while IFS= read -r line; do
  [ -z "$line" ] && continue
  uuid="$(printf '%s' "$line" | sed -n 's/.*"id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
  name="$(printf '%s' "$line" | sed -n 's/.*"name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
  arch="$(printf '%s' "$line" | sed -n 's/.*"goarch"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"

  if [ -z "$uuid" ]; then
    echo "notarize-poll: malformed manifest line (no id): $line" >&2
    exit 1
  fi

  resp="$(curl -sS -H "Authorization: Bearer $JWT" "$api/submissions/$uuid")"
  status="$(printf '%s' "$resp" | sed -n 's/.*"status"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"

  echo "notarize-poll: $name ($arch) uuid=$uuid status=${status:-UNKNOWN}"

  case "$status" in
    Accepted) ;;
    "In Progress")
      any_pending=1
      ;;
    Invalid|Rejected)
      any_rejected=1
      # Pull the developer log URL and dump it so the failure is
      # visible without a manual xcrun roundtrip.
      log_url="$(curl -sS -H "Authorization: Bearer $JWT" \
        "$api/submissions/$uuid/logs" \
        | sed -n 's/.*"developerLogUrl"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
      if [ -n "$log_url" ]; then
        echo "--- notarization log for $name ($arch / $uuid) ---"
        curl -sS "$log_url" || true
        echo "--- end log ---"
      fi
      ;;
    *)
      # Treat unknown statuses as pending so we don't tear down a
      # release on a transient API hiccup. The maintainer can re-run
      # the poll workflow manually if something is genuinely stuck.
      echo "notarize-poll: unrecognized status %q — treating as pending" >&2
      any_pending=1
      ;;
  esac
done < "$NOTARY_SUBMISSIONS_FILE"

if [ "$any_rejected" -eq 1 ]; then
  exit 1
fi
if [ "$any_pending" -eq 1 ]; then
  exit 2
fi
exit 0
