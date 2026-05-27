# jitenv zsh hook -- source via: eval "$(jitenv hook zsh)"
# Uses a zle widget bound to "accept-line" to rewrite BUFFER before
# submission. Only acts on commands whose first token resolves to /, ./
# or ../ paths.
#
# The runtime-dir + config-path values below are baked in by
# `jitenv hook zsh` at print time so the shell never duplicates the
# Go side's path resolution. See internal/shell/render.go.

if [[ -n "${__JITENV_LOADED:-}" ]]; then return 0; fi
__JITENV_LOADED=1

# Per-session nonce — used by jitenv run/shim to validate the
# __JITENV_INJECTED bypass marker (security #120). See bash.sh for
# the full rationale.
__JITENV_SESSION_NONCE="$( {
    head -c 16 /dev/urandom 2>/dev/null | od -An -tx1 | tr -d ' \n'
} || printf '%x%x%x%x' "$RANDOM" "$RANDOM" "$RANDOM" "$RANDOM")"
export __JITENV_SESSION_NONCE

__JITENV_RUNTIME_DIR={{RuntimeDir}}
__JITENV_CFG_PATH={{ConfigPath}}
export __JITENV_WRAP_DIR="$__JITENV_RUNTIME_DIR/shells/$$/bin"
# See bash.sh — gates env injection in the shim so vars don't leak
# into children of unmapped commands (issue #52).
export __JITENV_SHELL_PID=$$
# Restrict to 0700 across the full hierarchy. `mkdir -p -m` only
# sets the mode on the leaf; intermediates inherit the umask
# (typically 022 → 0755), which would trip the runtime-dir
# ownership check (security #117). A subshell with umask 077 makes
# every intermediate land at 0700.
(umask 077 && mkdir -p "$__JITENV_WRAP_DIR" 2>/dev/null)
case ":$PATH:" in
    *":$__JITENV_WRAP_DIR:"*) : ;;
    *) export PATH="$__JITENV_WRAP_DIR:$PATH" ;;
esac

# Tiny per-shell $JITENV_CONFIG override so users can re-point one
# shell at a different config without re-sourcing the hook. The
# baked-in default (see __JITENV_CFG_PATH above) is what `jitenv`
# itself resolves; this function exists so callers can query the
# effective config path from inside the shell.
__jitenv_cfg_path() {
    if [[ -n "${JITENV_CONFIG:-}" ]]; then
        print -r -- "$JITENV_CONFIG"
    else
        print -r -- "$__JITENV_CFG_PATH"
    fi
}
# precmd-style hook: fires every time the prompt is about to redraw,
# including after a cd. The Go side compares pwd and the config-file
# mtime against per-shell sidecar state ($__JITENV_RUNTIME_DIR/shells/
# $$/last-mtime + the wrapper-dir contents) and short-circuits when
# nothing changed. One fork per prompt; Go has nothing to do in the
# common case. Keeping the state in Go means a fresh re-source of the
# hook doesn't cause a spurious reconcile.
__jitenv_chpwd() {
    # No 2>/dev/null on purpose; see bash.sh for the rationale.
    jitenv __chpwd "$$" "${__JITENV_LAST_PWD-}" "$PWD"
    # Exit 10 means a wrapper was added or removed → rebuild zsh's
    # command hash table so the change takes effect immediately. Without
    # it, a wrapper added for an already-run command stays masked by the
    # cached path, and a just-removed wrapper leaves a dead hash entry.
    # See bash.sh for the full rationale. Capture $? first; the trailing
    # assignment resets it so we don't leak a non-zero status.
    local rc=$?
    [[ $rc -eq 10 ]] && rehash
    __JITENV_LAST_PWD="$PWD"
}
typeset -ga precmd_functions
precmd_functions+=(__jitenv_chpwd)
# Populate once at hook-load.
__jitenv_chpwd

# Version-check (#136): fire-and-forget background HTTP fetch
# refreshes a 24h-cached sidecar; the foreground __version_notice
# reads that cache and prints a one-line yellow notice if a newer
# release is known. Both are guarded server-side by
# JITENV_NO_VERSION_CHECK / CI / config / version!="dev"; the shell
# predicate below is a fork-saver.
#
# `2>&1 >/dev/null` on the notice (NOT `>/dev/null 2>&1`) silences
# stdout while keeping stderr on the terminal so the notice is
# visible. The background fetch silences both.
if [[ -t 2 && -z "${JITENV_NO_VERSION_CHECK:-}" && -z "${CI:-}" ]]; then
    ( jitenv __version_check & ) >/dev/null 2>&1
    jitenv __version_notice 2>&1 >/dev/null
fi

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
                    # ${(q+)resolved} produces a zsh-safe quoted form,
                    # so a filename containing `"`, `$`, backtick, or
                    # other zsh-active characters (legal on Linux/macOS)
                    # can't break BUFFER's quoting (security #123).
                    BUFFER="jitenv run ${(q+)resolved}$rest"
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
