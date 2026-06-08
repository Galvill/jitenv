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

# Per-session nonce — used by jitenv run/shim to validate the
# __JITENV_INJECTED bypass marker (security #120). Generated fresh
# here so a malicious pre-set __JITENV_INJECTED=1 from a hostile
# .bashrc / shell plugin / CI env doesn't silently disable secret
# injection. Falls back through /dev/urandom → $RANDOM if the
# higher-quality sources aren't reachable; even the fallback is
# unguessable to an attacker who doesn't share this shell's $RANDOM
# state.
__JITENV_SESSION_NONCE="$( {
    head -c 16 /dev/urandom 2>/dev/null | od -An -tx1 | tr -d ' \n'
} || printf '%x%x%x%x' "$RANDOM" "$RANDOM" "$RANDOM" "$RANDOM")"
export __JITENV_SESSION_NONCE

__JITENV_RUNTIME_DIR={{RuntimeDir}}
__JITENV_CFG_PATH={{ConfigPath}}
# Hot-path binary: the lightweight `jitenv-hook` when installed (≈1.5ms
# startup), else bare `jitenv` (≈50ms — it links the AWS SDK / net-http
# graph). Baked by `jitenv hook bash`; __chpwd / is-mapped / run go
# through it, the once-per-load version check stays on full `jitenv`.
__JITENV_BIN={{HookBin}}
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

# Ensure the wrap dir sits at the FRONT of PATH. Re-run from
# __jitenv_chpwd below so any later PATH prepend can't silently mask a
# wrapper symlink with a real binary of the same name. Concrete
# repro: Ubuntu's stock ~/.profile sources ~/.bashrc (running the
# hook → shim dir prepended) and THEN prepends ~/.local/bin (issue
# #224). A real `~/.local/bin/terraform` would otherwise win over our
# symlinked wrapper and secrets would silently not get injected.
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
# itself resolves; this function only exists so callers can query the
# effective config path from inside the shell.
__jitenv_cfg_path() {
    if [[ -n "${JITENV_CONFIG:-}" ]]; then
        printf '%s' "$JITENV_CONFIG"
    else
        printf '%s' "$__JITENV_CFG_PATH"
    fi
}
# Path/glob match anchors (issue #260). `jitenv __chpwd` writes these to
# a per-shell sidecar whenever the config changes; the DEBUG trap reads
# them to decide — with ZERO subprocess forks — whether a resolved
# command could possibly match a `path`/`glob` mapping. Without this the
# trap forks `jitenv is-mapped` for every command, e.g. every git-prompt
# command on every prompt. cwd_glob mappings are NOT anchors — those are
# served by the wrapper shims in $__JITENV_WRAP_DIR, not this path.
__JITENV_ANCHORS_FILE="${__JITENV_WRAP_DIR%/bin}/match-anchors"
declare -A __JITENV_EXACT 2>/dev/null
declare -a __JITENV_PREFIX 2>/dev/null
# Whether a BARE command could ever resolve (via $PATH) to a mapped file.
# Recomputed by __jitenv_load_anchors; gates the trap's bare-name branch
# so it doesn't `type -P` (walk $PATH) when nothing could match. (#263)
__JITENV_BARENAME_ACTIVE=0

# __jitenv_native_path sets __JITENV_NATIVE_PATH to $PATH with the WSL2
# Windows mounts (/mnt/*) stripped, so `type -P` in the trap never stats
# the slow 9P filesystem. path/glob mappings are native-fs files, so a
# bare command that maps can always still be found. No fork. (#263)
__jitenv_native_path() {
    local d IFS=:
    __JITENV_NATIVE_PATH=
    for d in $PATH; do
        case "$d" in /mnt/*) continue ;; esac
        __JITENV_NATIVE_PATH="${__JITENV_NATIVE_PATH:+$__JITENV_NATIVE_PATH:}$d"
    done
}

__jitenv_load_anchors() {
    __JITENV_EXACT=()
    __JITENV_PREFIX=()
    __JITENV_BARENAME_ACTIVE=0
    [[ -r "$__JITENV_ANCHORS_FILE" ]] || return 0
    local kind val
    # IFS=tab so values (absolute paths / prefixes) keep any spaces.
    while IFS=$'\t' read -r kind val; do
        case "$kind" in
            E) __JITENV_EXACT["$val"]=1 ;;
            P) __JITENV_PREFIX+=("$val") ;;
        esac
    done < "$__JITENV_ANCHORS_FILE"

    # A bare command resolves to <pathdir>/<name>, so it can only match an
    # exact anchor whose dir is on $PATH, or a glob whose literal prefix
    # overlaps a $PATH dir. If none do, the trap can skip the bare-name
    # resolve entirely — that's what keeps a git-prompt cheap on WSL2,
    # where each `type -P` would otherwise stat ~25 slow /mnt/* (9P) dirs.
    # /mnt/* entries are ignored here too (mappings are native files). (#263)
    local d pd p
    local -A _pd=()
    local IFS=:
    for d in $PATH; do
        case "$d" in /mnt/*) continue ;; esac
        _pd["$d"]=1
    done
    IFS=$' \t\n'
    for pd in "${!__JITENV_EXACT[@]}"; do
        [[ -n "${_pd["${pd%/*}"]:-}" ]] && { __JITENV_BARENAME_ACTIVE=1; return 0; }
    done
    for p in "${__JITENV_PREFIX[@]}"; do
        for d in "${!_pd[@]}"; do
            [[ "$d/" == "$p"* || "$p" == "$d/"* ]] && { __JITENV_BARENAME_ACTIVE=1; return 0; }
        done
    done
}

# chpwd hook: bash has no native chpwd, so we drive `jitenv __chpwd`
# from PROMPT_COMMAND. The Go side compares pwd and the config-file
# mtime against per-shell sidecar state ($__JITENV_RUNTIME_DIR/shells/
# $$/last-mtime + the wrapper-dir contents) and short-circuits when
# nothing changed. One fork per prompt; ~50us when Go has nothing to
# do. Keeping the state in Go means a fresh `eval "$(jitenv hook bash)"`
# doesn't cause a spurious reconcile.
__jitenv_chpwd() {
    # Keep the wrap dir at the front of PATH even if a downstream
    # startup file (e.g. ~/.profile's `~/.local/bin` prepend) shoved
    # it back (#224).
    __jitenv_ensure_path

    local base="${__JITENV_WRAP_DIR%/bin}"
    # Per-command injection marker (#182): drop it so the next typed
    # command re-injects from scratch. The `[[ -e ]]` test is a builtin
    # (no fork); `rm` only runs right after a mapped command actually set
    # the marker, never in the steady state.
    [[ -e "$base/injected" ]] && command rm -f "$base/injected" 2>/dev/null

    # In-shell short-circuit (#263): skip the `jitenv-hook __chpwd` fork
    # entirely when neither the cwd nor the config changed since our last
    # reconcile. A single fork/exec is ~17ms on WSL2, so forking every
    # prompt just to learn "nothing changed" is the dominant prompt cost.
    # `last-mtime` is the stamp jitenv writes on every reconcile; bash's
    # `-nt` is whole-second, so a config edit in the SAME wall-second as
    # the last reconcile is picked up on the next cd rather than the next
    # prompt (never wrong — a newly added mapping just activates a beat
    # late). The Go side keeps the authoritative nanosecond check for when
    # we DO fork. No config / no stamp yet → fall through and fork.
    local cfg="${JITENV_CONFIG:-$__JITENV_CFG_PATH}"
    if [[ "$PWD" == "${__JITENV_LAST_PWD-}" && -f "$base/last-mtime" \
          && ! "$cfg" -nt "$base/last-mtime" ]]; then
        return 0
    fi

    # No 2>/dev/null on purpose: the chpwd subcommand is silent
    # in normal operation (it only writes to stderr when
    # JITENV_HOOK_DEBUG is set). Swallowing stderr here would
    # hide debug diagnostics + a "jitenv: command not found"
    # if the binary ever falls off $PATH mid-session.
    "$__JITENV_BIN" __chpwd "$$" "${__JITENV_LAST_PWD-}" "$PWD"
    # Exit 10 means a wrapper was added or removed. Clear bash's
    # command-hash table so the change takes effect immediately: bash
    # caches command→path lookups, so a wrapper added for a command that
    # was already run keeps resolving to the original binary (secrets
    # silently not injected), and a wrapper just removed leaves a dead
    # hash entry that fails with "No such file or directory" (checkhash
    # is off by default). `hash -r` is cheap — the next lookup re-scans
    # $PATH. Capture $? first; the trailing assignment resets it to 0 so
    # we don't leak a non-zero status into the user's prompt.
    local rc=$?
    [[ $rc -eq 10 ]] && hash -r 2>/dev/null
    # Refresh the in-shell anchor cache (cheap: a builtin read of a tiny
    # file, no fork) — only after a reconcile, since anchors change only
    # when the config does.
    __jitenv_load_anchors
    __JITENV_LAST_PWD="$PWD"
}
# Run once at hook-load time so the wrapper dir is populated before
# the first command in this shell.
__jitenv_chpwd
PROMPT_COMMAND="__jitenv_chpwd${PROMPT_COMMAND:+;$PROMPT_COMMAND}"

# Version-check (#136): fire-and-forget background HTTP fetch
# refreshes a 24h-cached sidecar at $XDG_CACHE_HOME/jitenv/
# version_check.json; the foreground __version_notice reads that
# cache and prints a one-line yellow notice if a newer release is
# known. Both are guarded server-side by JITENV_NO_VERSION_CHECK /
# CI / config / version!="dev" — the shell predicate below is a
# fork-saver, not the source of truth.
#
# `2>&1 >/dev/null` on the notice (NOT `>/dev/null 2>&1`) silences
# stdout while keeping stderr on the terminal so the notice is
# visible. The background fetch silences both because nothing it
# writes is for the user.
if [[ -t 2 && -z "${JITENV_NO_VERSION_CHECK:-}" && -z "${CI:-}" ]]; then
    ( jitenv __version_check & ) >/dev/null 2>&1
    jitenv __version_notice 2>&1 >/dev/null
fi

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

    # Fast path: with no `path`/`glob` mappings the trap can never route
    # anything (cwd_glob is handled by the PATH wrappers), so skip all
    # resolution outright — not even a `type -P`. This is what keeps the
    # prompt cheap for cwd_glob-only / unmapped configs, where the DEBUG
    # trap otherwise fired a `jitenv is-mapped` fork for every command
    # bash runs while drawing the prompt. (issue #260)
    [[ ${#__JITENV_EXACT[@]} -eq 0 && ${#__JITENV_PREFIX[@]} -eq 0 ]] && return 0

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
    elif [[ "$first" = */* ]]; then
        # Any token containing a slash is a pathname (./x, ../x, dir/x,
        # a/b/c), not a $PATH lookup — resolve it relative to the cwd. This
        # is NOT gated by the bare-name PATH check below: a relative path
        # can match a path/glob mapping regardless of $PATH. (issue #237/#263)
        resolved="$(cd "$(dirname "$first")" 2>/dev/null && pwd)/$(basename "$first")"
    else
        # Bare name → resolve through $PATH so `path`/`glob` mappings fire
        # on PATH-invoked commands too, not just explicit-path ones.
        # (issue #237)
        #
        # Skip entirely unless an anchor is actually reachable via $PATH
        # (see __jitenv_load_anchors). A bare command can only resolve to a
        # mapped file whose dir is on $PATH; when none is, resolving here is
        # pure waste — and on WSL2 `type -P` would stat every /mnt/* (9P)
        # $PATH entry for each keyword/command a git-prompt runs, which is
        # what made prompts hang for tens of seconds. (issue #263)
        [[ ${__JITENV_BARENAME_ACTIVE:-0} -eq 1 ]] || return 0
        # Resolve through $PATH minus the WSL2 /mnt/* (9P) mounts so even
        # the active case never stats the slow Windows filesystem.
        # `type -P` only reports a real executable file (skips builtins,
        # aliases, functions, and typos, which yield an empty result →
        # run normally).
        __jitenv_native_path
        resolved="$(PATH="$__JITENV_NATIVE_PATH" builtin type -P -- "$first" 2>/dev/null)"
        [[ -z "$resolved" ]] && return 0
        # If the name resolved to a cwd_glob wrapper, the wrapper shim
        # already calls is-mapped itself — routing it through here too
        # would double-dispatch. Let the wrapper handle it.
        [[ "$resolved" == "$__JITENV_WRAP_DIR/"* ]] && return 0
    fi
    [[ ! -f "$resolved" ]] && return 0

    # Pre-filter: only spawn `jitenv is-mapped` when this resolved path
    # could actually match a path/glob mapping — an exact `path` target,
    # or under some `glob`'s literal prefix. Everything else (the bulk of
    # what runs, including a git-prompt's /usr/bin commands) returns here
    # with no fork. `is-mapped` stays the source of truth for whatever
    # passes; the prefix test is conservative (necessary condition for a
    # doublestar match), so it can only ever cause an extra fork that
    # is-mapped then rejects — never a missed injection. (issue #260)
    if [[ -z "${__JITENV_EXACT[$resolved]:-}" ]]; then
        local _maybe= _pfx
        if (( ${#__JITENV_PREFIX[@]} )); then
            for _pfx in "${__JITENV_PREFIX[@]}"; do
                [[ "$resolved" == "$_pfx"* ]] && { _maybe=1; break; }
            done
        fi
        [[ -z "$_maybe" ]] && return 0
    fi

    __jitenv_log "candidate cmd=[$cmd] resolved=[$resolved]"
    "$__JITENV_BIN" is-mapped "$resolved" >/dev/null 2>&1
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
            local quoted_resolved quoted_bin
            printf -v quoted_resolved '%q' "$resolved"
            printf -v quoted_bin '%q' "$__JITENV_BIN"
            eval "$quoted_bin run $quoted_resolved$rest"
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
