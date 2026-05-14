# jitenv PowerShell hook -- source via:
#   Invoke-Expression (& jitenv hook powershell | Out-String)
#
# Requires PowerShell 7+. Windows Server PowerShell 5.x is intentionally
# unsupported (see issue #39 design call). Drives the cwd_glob wrapper
# scheme only:
#   - Prepends the per-shell wrap dir to $env:PATH so PATHEXT picks up
#     the .ps1 wrappers that `jitenv __chpwd` populates.
#   - Wraps the user's `prompt` function so every new prompt fires
#     `jitenv __chpwd` to reconcile the wrap dir against the mapping
#     index. The cost is one fork per prompt; chpwd short-circuits in
#     ~50us when nothing changed.
#
# v1 does NOT intercept absolute-path commands. PowerShell has no DEBUG
# trap; a PSReadLine AcceptLine handler was rejected in #39 as too
# invasive. Users on Windows route through the cwd_glob shims.
#
# The runtime-dir + config-path values below are baked in by
# `jitenv hook powershell` at print time. See internal/shell/render.go.

if ($global:__JITENV_LOADED) { return }
$global:__JITENV_LOADED = $true

$global:__JITENV_RUNTIME_DIR = {{RuntimeDir}}
$global:__JITENV_CFG_PATH    = {{ConfigPath}}
# Recorded so the shim can tell "this shell typed the command" from
# "an unmapped descendant spawned the wrapped binary"; only the former
# should pull in mapped env vars (issue #52).
$global:__JITENV_SHELL_PID   = $PID
$global:__JITENV_WRAP_DIR    = Join-Path $__JITENV_RUNTIME_DIR (Join-Path 'shells' (Join-Path "$PID" 'bin'))
$global:__JITENV_LAST_PWD    = ''
$env:__JITENV_SHELL_PID      = "$PID"
$env:__JITENV_WRAP_DIR       = $__JITENV_WRAP_DIR

# Create the wrap dir up-front so the PATH prepend has a real target
# even before the first prompt fires. New-Item -Force is idempotent.
New-Item -ItemType Directory -Force -Path $__JITENV_WRAP_DIR | Out-Null

# Prepend, once per shell. PATHEXT must include .PS1 for the wrappers
# to resolve when the user types `npm` (default on pwsh 7).
#
# Use $env:PATH (upper-case) rather than $env:Path. On Windows pwsh
# env-var lookups are case-insensitive and either form works; on Linux
# pwsh they are case-sensitive and the env var is named PATH — the
# mixed-case form returns an empty string, which silently breaks both
# the contains-check and the assignment. Same applies elsewhere in
# this script.
$__jitenv_pathSeparator = [System.IO.Path]::PathSeparator
$__jitenv_existing = $env:PATH -split [regex]::Escape($__jitenv_pathSeparator)
if (-not ($__jitenv_existing -contains $__JITENV_WRAP_DIR)) {
    $env:PATH = $__JITENV_WRAP_DIR + $__jitenv_pathSeparator + $env:PATH
}
Remove-Variable __jitenv_pathSeparator, __jitenv_existing -ErrorAction SilentlyContinue

# Tiny per-shell $JITENV_CONFIG override so users can re-point one
# shell at a different config without re-sourcing the hook. The
# baked-in default (see __JITENV_CFG_PATH above) is what `jitenv`
# itself resolves; this function only exists so callers can query the
# effective config path from inside the shell.
function global:__jitenv_cfg_path {
    if ($env:JITENV_CONFIG) {
        return $env:JITENV_CONFIG
    }
    return $__JITENV_CFG_PATH
}

# chpwd: pwsh has no native chpwd event, so we drive `jitenv __chpwd`
# from the prompt function (the only hook that runs once per
# interactive submission). The Go side compares pwd and the config-file
# mtime against per-shell sidecar state and short-circuits when nothing
# changed. Keeping the state in Go means re-sourcing the hook doesn't
# cause a spurious reconcile.
$global:__JITENV_ORIG_PROMPT = $function:prompt

function global:__jitenv_chpwd {
    $cur = (Get-Location).Path
    # No 2>$null on purpose: the chpwd subcommand is silent in normal
    # operation (it only writes to stderr when JITENV_HOOK_DEBUG is
    # set). Swallowing stderr here would hide debug diagnostics and a
    # "jitenv: command not found" if the binary ever falls off PATH
    # mid-session. Errors are still trapped so the prompt never breaks.
    try {
        & jitenv __chpwd "$PID" $__JITENV_LAST_PWD $cur | Out-Null
    } catch {
        if ($env:JITENV_HOOK_DEBUG) {
            Write-Error $_
        }
    }
    $global:__JITENV_LAST_PWD = $cur
}

function global:prompt {
    __jitenv_chpwd
    if ($__JITENV_ORIG_PROMPT) {
        & $__JITENV_ORIG_PROMPT
    } else {
        "PS $((Get-Location).Path)> "
    }
}

# Run once at hook-load so the wrap dir is populated before the first
# command in this shell (matches bash/zsh behaviour).
__jitenv_chpwd

# The agent-down "Press Enter to skip, Ctrl+C to abort" countdown is
# implemented in Go (internal/agentwarn/agentwarn.go) and rendered by
# the cwd_glob shim. Nothing here needs to paint it.
