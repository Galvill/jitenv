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
    __JITENV_RUNTIME_DIR="$XDG_RUNTIME_DIR/jitenv"
else
    __JITENV_RUNTIME_DIR="${TMPDIR:-/tmp}/jitenv-$UID"
fi
export __JITENV_WRAP_DIR="$__JITENV_RUNTIME_DIR/shells/$$/bin"
mkdir -p "$__JITENV_WRAP_DIR" 2>/dev/null
case ":$PATH:" in
    *":$__JITENV_WRAP_DIR:"*) : ;;
    *) export PATH="$__JITENV_WRAP_DIR:$PATH" ;;
esac

# chpwd hook: bash has no native chpwd, so we run a tiny PWD-diff
# in PROMPT_COMMAND. The diff makes the per-prompt cost a single
# string compare; the full helper only fires on actual directory
# changes.
__JITENV_LAST_PWD="$PWD"
__jitenv_chpwd() {
    if [[ "$PWD" != "${__JITENV_LAST_PWD-}" ]]; then
        # No 2>/dev/null on purpose: the chpwd subcommand is silent
        # in normal operation (it only writes to stderr when
        # JITENV_HOOK_DEBUG is set). Swallowing stderr here would
        # hide both the debug diagnostics and a "jitenv: command not
        # found" if the binary ever falls off $PATH mid-session.
        jitenv __chpwd "$$" "${__JITENV_LAST_PWD-}" "$PWD"
        __JITENV_LAST_PWD="$PWD"
    fi
}
# Run once at hook-load time so the wrapper dir is populated before
# the first command in this shell.
jitenv __chpwd "$$" "" "$PWD"
PROMPT_COMMAND="__jitenv_chpwd${PROMPT_COMMAND:+;$PROMPT_COMMAND}"

shopt -s extdebug

# Print the "agent not loaded" warning in red and count down 10 seconds
# before returning. Ctrl+C during the countdown is caught and converted
# into a non-zero return so the caller can cancel the original command.
__jitenv_warn_no_agent() {
    local target="$1"
    local aborted=0
    trap 'aborted=1' INT

    local red=$'\033[1;31m'
    local reset=$'\033[0m'

    printf '%sjitenv agent is not loaded — env vars for %q will NOT be set.%s\n' \
        "$red" "$target" "$reset" >&2
    printf '%sWill run the command anyway in 10s. Press Enter to skip, Ctrl+C to abort.%s\n' \
        "$red" "$reset" >&2

    local total=${JITENV_HOOK_DELAY:-10}
    local i
    for ((i=total; i>0; i--)); do
        (( aborted )) && break
        printf '\r%s  %2ds remaining %s' "$red" "$i" "$reset" >&2
        # `read -t 1 -n 1` waits up to one second for one keystroke.
        # On Enter (or any key) it returns 0 and we skip; on timeout
        # it returns non-zero and we keep counting down.
        if read -t 1 -n 1 -s -r -p "" 2>/dev/null; then
            break
        fi
        (( aborted )) && break
    done

    trap - INT
    if (( aborted )); then
        printf '\n%saborted — command not executed.%s\n' "$red" "$reset" >&2
        return 1
    fi
    printf '\n' >&2
    return 0
}

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
