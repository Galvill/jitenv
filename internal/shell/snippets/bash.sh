# jitenv bash hook -- source via: eval "$(jitenv hook bash)"
# Requires bash 4+. Uses extdebug to cancel the original command and
# re-run the first token through `jitenv run` if it maps to a configured
# file. Only acts on commands whose first token resolves to /, ./ or ../
# paths (not PATH lookups) — keeps the hook fast and predictable.

if [[ -n "${__JITENV_LOADED:-}" ]]; then return 0 2>/dev/null || exit 0; fi
__JITENV_LOADED=1

shopt -s extdebug

__jitenv_debug_trap() {
    [[ -n "${__JITENV_REENTRY:-}" ]] && return 0

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

    if jitenv is-mapped "$resolved" >/dev/null 2>&1; then
        local rest="${cmd#"$first_raw"}"
        __JITENV_REENTRY=1
        eval "jitenv run \"$resolved\"$rest"
        unset __JITENV_REENTRY
        return 1
    fi
    return 0
}

trap '__jitenv_debug_trap' DEBUG
