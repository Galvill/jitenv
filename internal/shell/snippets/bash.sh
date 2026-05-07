# jitenv bash hook -- source via: eval "$(jitenv hook bash)"
# Requires bash 4+. Uses extdebug to cancel the original command and
# re-run the first token through `jitenv run` if it maps to a configured
# file. By default only acts on commands whose first token resolves to
# /, ./ or ../ paths (not PATH lookups) — keeps the hook fast and
# predictable. Bare PATH commands are checked against cwd_glob mappings
# only when the agent has flagged that at least one such mapping exists
# (a tiny sentinel file in $XDG_RUNTIME_DIR/jitenv/has-cwd, so the hook
# adds zero socket traffic for users who don't use the feature).

if [[ -n "${__JITENV_LOADED:-}" ]]; then return 0 2>/dev/null || exit 0; fi
__JITENV_LOADED=1

# Resolve the per-user runtime dir the same way the agent does
# (internal/agent/paths.go). Used to look up the has-cwd sentinel.
if [[ -n "${XDG_RUNTIME_DIR:-}" ]]; then
    __JITENV_RUNTIME_DIR="$XDG_RUNTIME_DIR/jitenv"
else
    __JITENV_RUNTIME_DIR="${TMPDIR:-/tmp}/jitenv-$UID"
fi

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
    printf '%sWill run the command anyway in 10s. Press Ctrl+C now to abort.%s\n' \
        "$red" "$reset" >&2

    local total=${JITENV_HOOK_DELAY:-10}
    local i
    for ((i=total; i>0; i--)); do
        (( aborted )) && break
        printf '\r%s  %2ds remaining %s' "$red" "$i" "$reset" >&2
        sleep 1 2>/dev/null
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
    # user just hit Tab. (issue #30)
    [[ -n "${COMP_LINE-}" || -n "${COMP_POINT-}" ]] && return 0

    # Agent-absence short-circuit. The DEBUG trap fires on every simple
    # command bash is about to execute — including the dozens that
    # PROMPT_COMMAND, prompt-side `$()`s, aliases, and `~/bin/...`
    # expansions inject between user keypresses. With the agent
    # deliberately locked, calling `jitenv is-mapped` on each of
    # those would either spam the red countdown (path-prefixed
    # commands) or pay a useless socket-dial-fail per command.
    # `jitenv lock` removes the agent socket on shutdown, so a single
    # stat takes us out of the trap entirely when there's no agent
    # to talk to. Mapped scripts run in this state will silently miss
    # their env vars; users can confirm with `jitenv status`.
    [[ -S "$__JITENV_RUNTIME_DIR/agent.sock" ]] || return 0

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
        # Bare PATH lookup. Only check if the agent has flagged at
        # least one cwd_glob mapping — the sentinel is a single file
        # the agent maintains, so absence costs one stat. With the
        # sentinel absent (the common case for users who don't use
        # cwd mappings) we exit immediately, preserving today's
        # zero-overhead behaviour.
        [[ -e "$__JITENV_RUNTIME_DIR/has-cwd" ]] || return 0
        __jitenv_log "candidate cmd=[$cmd] cwd=[$PWD] cmdname=[$first]"
        jitenv is-mapped --cwd "$PWD" --cmd "$first" >/dev/null 2>&1
        local rc=$?
        __jitenv_log "is-mapped (cwd) rc=$rc"
        case "$rc" in
            0)
                __jitenv_log "branch=cwd-case0 (mapped → jitenv run)"
                local rest="${cmd#"$first_raw"}"
                __JITENV_REENTRY=1
                eval "jitenv run --cwd \"$PWD\" --cmd \"$first\"$rest"
                unset __JITENV_REENTRY
                return 1
                ;;
            *)
                # rc=1 (no match) or rc=2 (agent unreachable). Bare
                # commands fire on every keystroke-completed command,
                # so silently fall through rather than spamming the
                # agent-unreachable warning the user gets for explicit
                # path-mapped runs.
                return 0
                ;;
        esac
    fi
    [[ ! -f "$resolved" ]] && return 0

    __jitenv_log "candidate cmd=[$cmd] resolved=[$resolved]"
    jitenv is-mapped "$resolved" >/dev/null 2>&1
    local rc=$?
    __jitenv_log "is-mapped rc=$rc"
    case "$rc" in
        0)
            __jitenv_log "branch=case0 (mapped → jitenv run)"
            local rest="${cmd#"$first_raw"}"
            __JITENV_REENTRY=1
            eval "jitenv run \"$resolved\"$rest"
            unset __JITENV_REENTRY
            return 1
            ;;
        2)
            __jitenv_log "branch=case2 (agent unreachable → warn)"
            # Agent unreachable. We can't tell whether the file is
            # mapped, but the user clearly intends to use jitenv (the
            # hook is installed) so warn loudly and offer an abort.
            if ! __jitenv_warn_no_agent "$resolved"; then
                return 1
            fi
            return 0
            ;;
        *)
            __jitenv_log "branch=case* (rc=$rc — unmapped, let it run)"
            return 0
            ;;
    esac
}

trap '__jitenv_debug_trap' DEBUG
