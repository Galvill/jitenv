# jitenv zsh hook -- source via: eval "$(jitenv hook zsh)"
# Uses a zle widget bound to "accept-line" to rewrite BUFFER before
# submission. Only acts on commands whose first token resolves to /, ./
# or ../ paths.

if [[ -n "${__JITENV_LOADED:-}" ]]; then return 0; fi
__JITENV_LOADED=1

# Per-shell wrapper-symlink dir for cwd_glob mappings (mirrors
# bash.sh).
if [[ -n "${XDG_RUNTIME_DIR:-}" ]]; then
    __JITENV_RUNTIME_DIR="${XDG_RUNTIME_DIR%/}/jitenv"
else
    __JITENV_RUNTIME_DIR="${TMPDIR:-/tmp}"
    __JITENV_RUNTIME_DIR="${__JITENV_RUNTIME_DIR%/}/jitenv-$UID"
fi
export __JITENV_WRAP_DIR="$__JITENV_RUNTIME_DIR/shells/$$/bin"
# See bash.sh — gates env injection in the shim so vars don't leak
# into children of unmapped commands (issue #52).
export __JITENV_SHELL_PID=$$
mkdir -p "$__JITENV_WRAP_DIR" 2>/dev/null
case ":$PATH:" in
    *":$__JITENV_WRAP_DIR:"*) : ;;
    *) export PATH="$__JITENV_WRAP_DIR:$PATH" ;;
esac

__jitenv_cfg_path() {
    if [[ -n "${JITENV_CONFIG:-}" ]]; then
        print -r -- "$JITENV_CONFIG"
    else
        print -r -- "${XDG_CONFIG_HOME:-$HOME/.config}/jitenv/config.toml"
    fi
}
__jitenv_cfg_mtime() {
    local cfg="$1"
    [[ -e "$cfg" ]] || { print -r -- 0; return; }
    stat -c %Y "$cfg" 2>/dev/null || stat -f %m "$cfg" 2>/dev/null || print -r -- 0
}
# precmd-style hook: fires every time the prompt is about to redraw,
# including after a cd (subsumes chpwd_functions). The PWD-diff +
# mtime-diff inside makes the no-op case a tiny pair of comparisons.
# The mtime branch handles "user edits config while sitting in the
# mapped dir": symlinks get re-reconciled on the next prompt without
# requiring a cd.
__jitenv_chpwd() {
    local cfg mtime
    cfg="$(__jitenv_cfg_path)"
    mtime="$(__jitenv_cfg_mtime "$cfg")"
    if [[ "$PWD" != "${__JITENV_LAST_PWD-}" || "$mtime" != "${__JITENV_LAST_CFG_MTIME-}" ]]; then
        # No 2>/dev/null on purpose; see bash.sh for the rationale.
        jitenv __chpwd "$$" "${__JITENV_LAST_PWD-}" "$PWD"
        __JITENV_LAST_PWD="$PWD"
        __JITENV_LAST_CFG_MTIME="$mtime"
    fi
}
typeset -ga precmd_functions
precmd_functions+=(__jitenv_chpwd)
# Populate once at hook-load.
__jitenv_chpwd

# The agent-down "Press Enter to skip, Ctrl+C to abort" countdown is
# implemented in Go (internal/agentwarn/agentwarn.go) and rendered by
# `jitenv run` / the shim. Nothing in the shell hook needs to paint
# it — the hook just routes through `jitenv run` and lets the Go side
# handle the UX uniformly across path-mapped and cwd_glob flows.

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
                    # Mapped. `jitenv run` paints its own warning +
                    # countdown when the agent is locked, so we just
                    # rewrite BUFFER and let it handle everything.
                    __jitenv_log "branch=case0 (mapped → jitenv run)"
                    BUFFER="jitenv run \"$resolved\"$rest"
                    ;;
                *)
                    # rc=1 (not mapped) or rc=2 (config unreadable) →
                    # run the user's command unchanged.
                    __jitenv_log "branch=case* (rc=$rc — let it run)"
                    ;;
            esac
        fi
    fi
    zle .accept-line
}
zle -N accept-line __jitenv_accept_line
