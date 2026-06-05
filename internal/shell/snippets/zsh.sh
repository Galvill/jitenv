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
# Hot-path binary: lightweight `jitenv-hook` when installed (≈1.5ms
# startup), else bare `jitenv`. See bash.sh for the rationale.
__JITENV_BIN={{HookBin}}
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

# Ensure the wrap dir sits at the FRONT of PATH. Re-run from
# __jitenv_chpwd below so any later PATH prepend can't silently mask a
# wrapper symlink with a real binary of the same name. See bash.sh
# (#224) for the concrete Ubuntu-stock-~/.profile repro.
__jitenv_ensure_path() {
    case ":$PATH:" in
        ":$__JITENV_WRAP_DIR:"*) return 0 ;;
    esac
    local p=":$PATH:"
    p="${p//:$__JITENV_WRAP_DIR:/:}"
    p="${p#:}"; p="${p%:}"
    export PATH="$__JITENV_WRAP_DIR:$p"
}
__jitenv_ensure_path

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
# Path/glob match anchors (issue #260). See bash.sh for the full
# rationale: `jitenv __chpwd` writes a per-shell sidecar of the path/glob
# mapping anchors so the accept-line widget can decide, WITHOUT forking
# `jitenv is-mapped`, whether a typed command could match. cwd_glob is
# served by the PATH wrappers, not here.
__JITENV_ANCHORS_FILE="${__JITENV_WRAP_DIR%/bin}/match-anchors"
typeset -gA __JITENV_EXACT
typeset -ga __JITENV_PREFIX
__jitenv_load_anchors() {
    __JITENV_EXACT=()
    __JITENV_PREFIX=()
    [[ -r "$__JITENV_ANCHORS_FILE" ]] || return 0
    local kind val
    while IFS=$'\t' read -r kind val; do
        case "$kind" in
            E) __JITENV_EXACT[$val]=1 ;;
            P) __JITENV_PREFIX+=("$val") ;;
        esac
    done < "$__JITENV_ANCHORS_FILE"
}

# precmd-style hook: fires every time the prompt is about to redraw,
# including after a cd. The Go side compares pwd and the config-file
# mtime against per-shell sidecar state ($__JITENV_RUNTIME_DIR/shells/
# $$/last-mtime + the wrapper-dir contents) and short-circuits when
# nothing changed. One fork per prompt; Go has nothing to do in the
# common case. Keeping the state in Go means a fresh re-source of the
# hook doesn't cause a spurious reconcile.
__jitenv_chpwd() {
    # Keep the wrap dir at the front of PATH even if a downstream
    # startup file (e.g. ~/.zprofile prepends) shoved it back (#224).
    __jitenv_ensure_path

    local base="${__JITENV_WRAP_DIR%/bin}"
    # Per-command injection marker (#182): builtin test; rm only after a
    # mapped command actually set it. See bash.sh.
    [[ -e "$base/injected" ]] && command rm -f "$base/injected" 2>/dev/null

    # In-shell short-circuit (#263): skip the fork when neither cwd nor
    # config changed since the last reconcile. A fork/exec is ~17ms on
    # WSL2, so forking every prompt to learn "nothing changed" is the main
    # prompt cost. `-nt` is whole-second; a same-wall-second config edit
    # is caught on the next cd, not the next prompt (never wrong). See
    # bash.sh for the full rationale.
    local cfg="${JITENV_CONFIG:-$__JITENV_CFG_PATH}"
    if [[ "$PWD" == "${__JITENV_LAST_PWD-}" && -f "$base/last-mtime" \
          && ! "$cfg" -nt "$base/last-mtime" ]]; then
        return 0
    fi

    # No 2>/dev/null on purpose; see bash.sh for the rationale.
    "$__JITENV_BIN" __chpwd "$$" "${__JITENV_LAST_PWD-}" "$PWD"
    # Exit 10 means a wrapper was added or removed → rebuild zsh's
    # command hash table so the change takes effect immediately. Without
    # it, a wrapper added for an already-run command stays masked by the
    # cached path, and a just-removed wrapper leaves a dead hash entry.
    # See bash.sh for the full rationale. Capture $? first; the trailing
    # assignment resets it so we don't leak a non-zero status.
    local rc=$?
    [[ $rc -eq 10 ]] && rehash
    # Refresh the in-shell anchor cache (cheap builtin read, no fork) —
    # only after a reconcile, since anchors change only when config does.
    __jitenv_load_anchors
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

# __jitenv_anchor_match returns 0 if the resolved path could match a
# path/glob mapping (an exact target, or under some glob's literal
# prefix), so the widget only forks `jitenv is-mapped` for plausible
# candidates. Empty anchor sets (no path/glob mappings — e.g. cwd_glob
# only) never match → no fork. Conservative: a prefix hit can only cause
# an extra is-mapped fork that then declines, never a missed injection.
# (issue #260)
__jitenv_anchor_match() {
    local r="$1" pfx
    [[ -n "${__JITENV_EXACT[$r]}" ]] && return 0
    for pfx in "${__JITENV_PREFIX[@]}"; do
        [[ "$r" == "$pfx"* ]] && return 0
    done
    return 1
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
            ./*|../*)
                # Normalize the same way bash.sh does so zsh and bash
                # produce identical canonical absolute paths and stay in
                # sync. The old `${PWD}/${first#./}` only stripped a
                # leading `./`, so `../foo` became `$PWD/../foo` — an
                # unnormalized path that won't path-equality-match a
                # mapping stored in canonical absolute form (issue #245).
                resolved="$(cd "$(dirname "$first")" 2>/dev/null && pwd)/$(basename "$first")"
                ;;
            *)
                # Bare name → resolve through $PATH so `path`/`glob`
                # mappings fire on PATH-invoked commands too, not just
                # explicit-path ones. `whence -p` reports only a real
                # executable file (skips builtins, aliases, functions,
                # and typos, which yield an empty result → run normally).
                # If it resolves to a cwd_glob wrapper, the wrapper shim
                # already calls is-mapped itself, so don't double-dispatch
                # — leave resolved empty and let the wrapper handle it.
                # (issue #237)
                resolved="$(whence -p -- "$first" 2>/dev/null)"
                if [[ "$resolved" == "$__JITENV_WRAP_DIR/"* ]]; then
                    resolved=""
                fi
                ;;
        esac
        if [[ -n "$resolved" && -f "$resolved" ]] && __jitenv_anchor_match "$resolved"; then
            __jitenv_log "candidate cmd=[$BUFFER] resolved=[$resolved]"
            "$__JITENV_BIN" is-mapped "$resolved" >/dev/null 2>&1
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
                    BUFFER="${(q+)__JITENV_BIN} run ${(q+)resolved}$rest"
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
