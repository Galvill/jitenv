# jitenv bash hook -- source via: eval "$(jitenv hook bash)"
# Requires bash 4+. Uses extdebug to cancel the original command and
# re-run the first token through `jitenv run` if it maps to a configured
# file. Only acts on commands whose first token resolves to /, ./ or ../
# paths (not PATH lookups) — keeps the hook fast and predictable.

if [[ -n "${__JITENV_LOADED:-}" ]]; then return 0 2>/dev/null || exit 0; fi
__JITENV_LOADED=1

# Per-shell wrapper-symlink dir. The chpwd helper populates it on
# directory changes that hit a cwd_glob mapping; an empty dir is a
# silent fall-through. We pre-create it and prepend to $PATH once,
# right here, so PATH stays stable for the rest of the shell's life.
if [[ -n "${XDG_RUNTIME_DIR:-}" ]]; then
    # Strip a trailing slash so the concat doesn't yield "//jitenv"
    # for users whose XDG_RUNTIME_DIR happens to end in /. The Go
    # side normalises with filepath.Join; the shell needs to do it
    # by hand or the wrapper dir lands in $PATH with double slashes.
    __JITENV_RUNTIME_DIR="${XDG_RUNTIME_DIR%/}/jitenv"
else
    __JITENV_RUNTIME_DIR="${TMPDIR:-/tmp}"
    __JITENV_RUNTIME_DIR="${__JITENV_RUNTIME_DIR%/}/jitenv-$UID"
fi
export __JITENV_WRAP_DIR="$__JITENV_RUNTIME_DIR/shells/$$/bin"
# Recorded so the shim can tell "this shell typed the command" from
# "an unmapped descendant spawned the wrapped binary"; only the former
# should pull in mapped env vars (issue #52).
export __JITENV_SHELL_PID=$$
mkdir -p "$__JITENV_WRAP_DIR" 2>/dev/null
case ":$PATH:" in
    *":$__JITENV_WRAP_DIR:"*) : ;;
    *) export PATH="$__JITENV_WRAP_DIR:$PATH" ;;
esac

# chpwd hook: bash has no native chpwd, so we run a tiny PWD-diff
# in PROMPT_COMMAND. The diff makes the per-prompt cost a single
# string compare; the full helper only fires on actual directory
# changes.
# Resolve the config-file path the same way the Go side does.
__jitenv_cfg_path() {
    if [[ -n "${JITENV_CONFIG:-}" ]]; then
        printf '%s' "$JITENV_CONFIG"
    else
        printf '%s' "${XDG_CONFIG_HOME:-$HOME/.config}/jitenv/config.toml"
    fi
}
# stat -c is GNU; -f is BSD/macOS. Echo 0 on any failure so the
# comparison treats "missing" identically across runs.
__jitenv_cfg_mtime() {
    local cfg="$1"
    [[ -e "$cfg" ]] || { printf '0'; return; }
    stat -c %Y "$cfg" 2>/dev/null || stat -f %m "$cfg" 2>/dev/null || printf '0'
}
__jitenv_chpwd() {
    local cfg mtime
    cfg="$(__jitenv_cfg_path)"
    mtime="$(__jitenv_cfg_mtime "$cfg")"
    # Fire when EITHER the directory changed OR the config file's
    # mtime changed since the last fire. The mtime branch handles
    # "user edits config while sitting in the mapped dir": symlinks
    # get re-reconciled on the next prompt without requiring a cd.
    if [[ "$PWD" != "${__JITENV_LAST_PWD-}" || "$mtime" != "${__JITENV_LAST_CFG_MTIME-}" ]]; then
        # No 2>/dev/null on purpose: the chpwd subcommand is silent
        # in normal operation (it only writes to stderr when
        # JITENV_HOOK_DEBUG is set). Swallowing stderr here would
        # hide debug diagnostics + a "jitenv: command not found"
        # if the binary ever falls off $PATH mid-session.
        jitenv __chpwd "$$" "${__JITENV_LAST_PWD-}" "$PWD"
        __JITENV_LAST_PWD="$PWD"
        __JITENV_LAST_CFG_MTIME="$mtime"
    fi
}
# Run once at hook-load time so the wrapper dir is populated before
# the first command in this shell.
__jitenv_chpwd
PROMPT_COMMAND="__jitenv_chpwd${PROMPT_COMMAND:+;$PROMPT_COMMAND}"

shopt -s extdebug

# The agent-down "Press Enter to skip, Ctrl+C to abort" countdown is
# implemented in Go (internal/agentwarn/agentwarn.go) and rendered by
# `jitenv run` / the shim. Nothing in the shell hook needs to paint
# it — the hook just routes through `jitenv run` and lets the Go side
# handle the UX uniformly across path-mapped and cwd_glob flows.

# Set JITENV_HOOK_DEBUG=1 to log each branch the trap takes.
__jitenv_log() {
    [[ -n "${JITENV_HOOK_DEBUG:-}" ]] && printf 'jitenv-hook: %s\n' "$*" >&2
}

__jitenv_debug_trap() {
    [[ -n "${__JITENV_REENTRY:-}" ]] && return 0

    # Skip while bash is running a programmable-completion function —
    # the DEBUG trap fires on commands inside compfuncs too, and we
    # don't want to paint the agent-unreachable countdown when the
    # user just hit Tab. COMP_LINE / COMP_POINT are only set during
    # completion, so they're a clean signal. (issue #30)
    [[ -n "${COMP_LINE-}" || -n "${COMP_POINT-}" ]] && return 0

    # Skip when bash is running its command-not-found handler. On
    # Debian/Ubuntu the handler shells out to /usr/lib/command-not-found
    # to suggest an apt package; that path-prefixed call would
    # otherwise hit the warn branch with a locked agent and paint the
    # countdown for every typo. The handler runs as the bash function
    # `command_not_found_handle` (or `_handler` on some distros), so
    # FUNCNAME shows it on the call stack while the trap fires.
    case " ${FUNCNAME[*]} " in
        *" command_not_found_handle "*|*" command_not_found_handler "*) return 0 ;;
    esac

    local cmd="$BASH_COMMAND"
    local first_raw; first_raw="${cmd%% *}"
    [[ -z "$first_raw" ]] && return 0
    # Dequoted form for path matching; the raw form is used to strip the
    # original first token off cmd to recover the rest of the args.
    local first="$first_raw"
    first="${first#\"}"; first="${first%\"}"
    first="${first#\'}"; first="${first%\'}"

    local resolved
    if [[ "$first" = /* ]]; then
        resolved="$first"
    elif [[ "$first" = ./* || "$first" = ../* ]]; then
        resolved="$(cd "$(dirname "$first")" 2>/dev/null && pwd)/$(basename "$first")"
    else
        return 0
    fi
    [[ ! -f "$resolved" ]] && return 0

    __jitenv_log "candidate cmd=[$cmd] resolved=[$resolved]"
    jitenv is-mapped "$resolved" >/dev/null 2>&1
    local rc=$?
    __jitenv_log "is-mapped rc=$rc"
    case "$rc" in
        0)
            # Mapped — `jitenv run` handles env injection and the
            # locked-agent UX (warn + countdown + run-with-parent-env)
            # internally, so we just delegate.
            __jitenv_log "branch=case0 (mapped → jitenv run)"
            local rest="${cmd#"$first_raw"}"
            __JITENV_REENTRY=1
            eval "jitenv run \"$resolved\"$rest"
            unset __JITENV_REENTRY
            return 1
            ;;
        2)
            # Config unreadable. Different from "agent locked" — the
            # latter no longer reaches this branch because is-mapped
            # reads config directly. Warn once and let the command
            # run; the user's jitenv install is broken in some way.
            __jitenv_log "branch=case2 (config unreadable — letting command run)"
            return 0
            ;;
        *)
            __jitenv_log "branch=case* (rc=$rc — unmapped, let it run)"
            return 0
            ;;
    esac
}

trap '__jitenv_debug_trap' DEBUG
