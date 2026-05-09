#!/bin/sh
# preremove script run by dpkg/rpm before `jitenv` is uninstalled.
#
# Runs as root, so we can't reach into a user's $HOME to clean up the
# hook line. Print a reminder on a real removal so the user knows
# there's manual cleanup left to do — but stay silent on upgrades.

set -e

# dpkg passes "remove" / "upgrade <ver>" / "failed-upgrade <ver>" as $1.
# rpm passes a numeric arg: "0" = final removal, "1" = upgrade.
case "${1:-}" in
    upgrade*|failed-upgrade*|1)
        exit 0
        ;;
esac

cat <<'EOF'

Removing jitenv. The shell hook line in ~/.bashrc / ~/.zshrc was added
by `jitenv hook install` and is NOT removed automatically — edit those
files and delete:

    eval "$(jitenv hook bash)"      # or `... zsh`

along with the "# jitenv: …" comment line above it. Bash users who let
the installer add a guarded `. ~/.bashrc` line to ~/.bash_profile /
~/.profile may want to remove that line too if jitenv was the only
reason for it.

EOF

exit 0
