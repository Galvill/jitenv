#!/bin/sh
# postinstall script run by dpkg/rpm after `jitenv` is unpacked.
#
# Runs as root, so it cannot safely write into a user's $HOME. We print
# a clear one-liner instead. `jitenv hook install` is itself idempotent
# (see internal/shell/install.go), so re-running is always safe.

set -e

cat <<'EOF'

jitenv installed.

To activate the shell hook (env-var injection on mapped commands), run
ONCE as your normal user (not as root):

    jitenv hook install

Then open a new shell. This appends a single eval line to your
~/.bashrc or ~/.zshrc and is idempotent on re-run.

To remove the hook later, edit ~/.bashrc / ~/.zshrc and delete the
line `eval "$(jitenv hook bash)"` (or `... zsh`) along with its
preceding "# jitenv: …" comment.

EOF

exit 0
