# jitenv bash hook -- source via: eval "$(jitenv hook bash)"
# Requires bash 4+. Uses extdebug to cancel the original command and
# re-run the first token through `jitenv run` if it maps to a configured
# file. Only acts on commands whose first token resolves to /, ./ or ../
# paths (not PATH lookups) — keeps the hook fast and predictable.
#
# The runtime-dir + config-path values below are baked in by
# `jitenv hook bash` at print time so the shell never duplicates the
# Go side's path resolution. See internal/shell/render.go.

if [[ -n "${__JITENV_LOADED:-}" ]]; then return 0 2>/dev/null || exit 0; fi
__JITENV_LOADED=1

__JITENV_RUNTIME_DIR={{RuntimeDir}}
__JITENV_CFG_PATH={{ConfigPath}}
export __JITENV_WRAP_DIR="$__JITENV_RUNTIME_DIR/shells/$$/bin"
# Recorded so the shim can tell "this shell typed the command" from
# "an unmapped descendant spawned the wrapped binary"; only the former
# should pull in mapped env vars (issue #52).
export __JITENV_SHELL_PID=$$
# Restrict to 0700 across the full hierarchy. Bash's `mkdir -p -m`
# only sets the mode on the leaf; intermediates inherit the umask
# (typically 022 → 0755), which would then trip the runtime-dir
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
# itself resolves; this function only exists so callers can query the
# effective config path from inside the shell.
__jitenv_cfg_path() {
    if [[ -n "${JITENV_CONFIG:-}" ]]; then
        printf '%s' "$JITENV_CONFIG"
    else
        printf '%s' "$__JITENV_CFG_PATH"
    fi
}
# chpwd hook: bash has no native chpwd, so we drive `jitenv __chpwd`
# from PROMPT_COMMAND. The Go side compares pwd and the config-file
# mtime against per-shell sidecar state ($__JITENV_RUNTIME_DIR/shells/
# $$/last-mtime + the wrapper-dir contents) and short-circuits when
# nothing changed. One fork per prompt; ~50us when Go has nothing to
# do. Keeping the state in Go means a fresh `eval "$(jitenv hook bash)"`
# doesn't cause a spurious reconcile.
__jitenv_chpwd() {
    # No 2>/dev/null on purpose: the chpwd subcommand is silent
    # in normal operation (it only writes to stderr when
    # JITENV_HOOK_DEBUG is set). Swallowing stderr here would
    # hide debug diagnostics + a "jitenv: command not found"
    # if the binary ever falls off $PATH mid-session.
    jitenv __chpwd "$$" "${__JITENV_LAST_PWD-}" "$PWD"
    __JITENV_LAST_PWD="$PWD"
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
            # printf %q produces a shell-safe quoted form of the path,
            # so a filename containing `"`, `$`, backtick, or other
            # bash-active characters (legal on Linux) can't break the
            # eval string's quoting (security #123).
            local quoted_resolved
            printf -v quoted_resolved '%q' "$resolved"
            eval "jitenv run $quoted_resolved$rest"
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
