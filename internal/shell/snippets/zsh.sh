# jitenv zsh hook -- source via: eval "$(jitenv hook zsh)"
# Uses a zle widget bound to "accept-line" to rewrite BUFFER before
# submission. Only acts on commands whose first token resolves to /, ./
# or ../ paths.

if [[ -n "${__JITENV_LOADED:-}" ]]; then return 0; fi
__JITENV_LOADED=1

# Warn loudly when the agent isn't reachable, count down 10 seconds,
# and let Ctrl+C abort. Returns non-zero on abort so the caller's `&&`
# short-circuits the actual command.
__jitenv_warn_no_agent() {
    emulate -L zsh
    local target="$1"
    local aborted=0
    trap 'aborted=1' INT

    local red=$'\033[1;31m'
    local reset=$'\033[0m'

    printf '%sjitenv agent is not loaded — env vars for %q will NOT be set.%s\n' \
        "$red" "$target" "$reset" >&2
    printf '%sWill run the command anyway in 10s. Press Ctrl+C now to abort.%s\n' \
        "$red" "$reset" >&2

    local total=${JITENV_HOOK_DELAY:-10}
    local i
    for ((i=total; i>0; i--)); do
        (( aborted )) && break
        printf '\r%s  %2ds remaining %s' "$red" "$i" "$reset" >&2
        sleep 1
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

# Set JITENV_HOOK_DEBUG=1 to log each branch the widget takes.
__jitenv_log() {
    [[ -n "${JITENV_HOOK_DEBUG:-}" ]] && printf 'jitenv-hook: %s\n' "$*" >&2
}

__jitenv_accept_line() {
    emulate -L zsh
    local first_raw first rest resolved
    first_raw="${BUFFER%% *}"
    rest="${BUFFER#$first_raw}"
    first="$first_raw"

    if [[ -n "$first" ]]; then
        first="${first#\"}"; first="${first%\"}"
        first="${first#\'}"; first="${first%\'}"
        case "$first" in
            /*)        resolved="$first" ;;
            ./*|../*)  resolved="${PWD}/${first#./}" ;;
            *)         resolved="" ;;
        esac
        if [[ -n "$resolved" && -f "$resolved" ]]; then
            __jitenv_log "candidate cmd=[$BUFFER] resolved=[$resolved]"
            jitenv is-mapped "$resolved" >/dev/null 2>&1
            local rc=$?
            __jitenv_log "is-mapped rc=$rc"
            case "$rc" in
                0)
                    __jitenv_log "branch=case0 (mapped → jitenv run)"
                    BUFFER="jitenv run \"$resolved\"$rest"
                    ;;
                2)
                    __jitenv_log "branch=case2 (agent unreachable → warn)"
                    # Agent unreachable — wrap the user's command so the
                    # warning + 10s grace runs first. && short-circuits
                    # the real command if the user aborts via Ctrl+C.
                    BUFFER="__jitenv_warn_no_agent \"$resolved\" && { $BUFFER ; }"
                    ;;
                *)
                    __jitenv_log "branch=case* (rc=$rc — unmapped, let it run)"
                    ;;
            esac
        fi
    fi
    zle .accept-line
}
zle -N accept-line __jitenv_accept_line
