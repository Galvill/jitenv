# jitenv zsh hook -- source via: eval "$(jitenv hook zsh)"
# Uses a zle widget bound to "accept-line" to rewrite BUFFER before
# submission. Only acts on commands whose first token resolves to /, ./
# or ../ paths.

if [[ -n "${__JITENV_LOADED:-}" ]]; then return 0; fi
__JITENV_LOADED=1

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
            if jitenv is-mapped "$resolved" >/dev/null 2>&1; then
                BUFFER="jitenv run \"$resolved\"$rest"
            fi
        fi
    fi
    zle .accept-line
}
zle -N accept-line __jitenv_accept_line
