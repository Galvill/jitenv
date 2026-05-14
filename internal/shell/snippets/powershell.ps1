# jitenv PowerShell hook -- source via:
#   Invoke-Expression (& jitenv hook powershell | Out-String)
#
# Requires PowerShell 7+. Windows Server PowerShell 5.x is intentionally
# unsupported (see issue #39 design call). Drives three flows:
#   - cwd_glob: prepends a per-shell wrap dir to $env:PATH and wraps the
#     `prompt` function so every prompt-fire reconciles the dir against
#     the mapping index via `jitenv __chpwd`.
#   - path / glob: a PSReadLine AcceptLine handler intercepts commands
#     whose first token resolves to an absolute or ./..-relative path;
#     when `jitenv is-mapped` returns 0, the line is rewritten to
#     `jitenv run "<path>" <rest>` so the file is exec'd with merged
#     env. Requires PSReadLine (default in pwsh 7+); without it the
#     interception silently no-ops and cwd_glob still works. Issues #103
#     (path) / #104 (glob).
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
# the cwd_glob shim and `jitenv run`. Nothing here needs to paint it.

# Strip one matching pair of surrounding quotes ('foo' / "foo"). Used
# by the AcceptLine rewrite to mirror the zsh widget's case-handling.
function global:__jitenv_unquote {
    param([string]$s)
    if (-not $s -or $s.Length -lt 2) { return $s }
    $first = $s[0]
    $last = $s[$s.Length - 1]
    if (($first -eq '"' -and $last -eq '"') -or
        ($first -eq "'" -and $last -eq "'")) {
        return $s.Substring(1, $s.Length - 2)
    }
    return $s
}

# Resolve a typed first token to an absolute filesystem path, or $null
# when it isn't path-shaped. Only ./..-relative and rooted paths are
# treated as commands the hook should intercept; bare names fall
# through to the existing $PATH/cwd_glob flow unchanged (issue #52).
# IsPathRooted handles both Unix `/foo` and Windows `C:\foo` /
# `C:/foo` forms.
function global:__jitenv_resolve_path {
    param([string]$first)
    if (-not $first) { return $null }
    if ([System.IO.Path]::IsPathRooted($first)) { return $first }
    if ($first.StartsWith('./') -or $first.StartsWith('../') -or
        $first.StartsWith('.\') -or $first.StartsWith('..\')) {
        return (Join-Path (Get-Location).Path $first)
    }
    return $null
}

# Pure rewrite function: given a typed command buffer, return the
# rewritten buffer (`jitenv run "<path>"<rest>`) when the first token
# resolves to a mapped file, otherwise return the buffer unchanged.
# Factored out of the AcceptLine handler so it can be unit-tested
# without instantiating PSReadLine — mirrors the zsh widget's
# direct-call-with-stubbed-zle pattern in e2e/scenarios/.
function global:__jitenv_rewrite_buffer {
    param([string]$buffer)
    if (-not $buffer) { return $buffer }
    # Split on the first run of ASCII whitespace. Simple shell-style
    # tokenisation matches what the user typed; full PSParser would be
    # overkill and would diverge from the zsh widget's behaviour.
    $i = $buffer.IndexOfAny([char[]]@([char]32, [char]9))
    if ($i -lt 0) {
        $first = $buffer
        $rest = ''
    } else {
        $first = $buffer.Substring(0, $i)
        $rest = $buffer.Substring($i)
    }
    $first = __jitenv_unquote $first
    $resolved = __jitenv_resolve_path $first
    if (-not $resolved) { return $buffer }
    if (-not (Test-Path -PathType Leaf -LiteralPath $resolved)) { return $buffer }
    & jitenv is-mapped $resolved 2>$null | Out-Null
    $rc = $LASTEXITCODE
    if ($env:JITENV_HOOK_DEBUG) {
        [Console]::Error.WriteLine("jitenv-hook: candidate=$buffer resolved=$resolved is-mapped=$rc")
    }
    # Exit code contract (see internal/cli/ismapped.go):
    #   0 → mapped, route through `jitenv run`
    #   1 → not mapped, leave the buffer alone
    #   2 → config unreadable, leave the buffer alone (matches bash hook)
    if ($rc -eq 0) {
        return ('jitenv run "{0}"{1}' -f $resolved, $rest)
    }
    return $buffer
}

# PSReadLine AcceptLine binding. Fires on Enter while the line editor
# is active; non-interactive `pwsh -Command` invocations never hit it,
# so jitenv-driven scripts continue to run unchanged.
#
# Guarded by Get-Command so the snippet is safe in PSReadLine-less
# environments (Remove-Module PSReadLine, constrained-language mode,
# very stripped Windows images). When PSReadLine is absent, only the
# cwd_glob flow remains — same as the v1 cutoff.
if (Get-Command Set-PSReadLineKeyHandler -ErrorAction SilentlyContinue) {
    Set-PSReadLineKeyHandler -Chord Enter -BriefDescription 'jitenv-accept-line' -LongDescription 'Route mapped paths through `jitenv run` before submitting.' -ScriptBlock {
        param($key, $arg)
        [string]$buffer = $null
        [int]$cursor = 0
        [Microsoft.PowerShell.PSConsoleReadLine]::GetBufferState([ref]$buffer, [ref]$cursor)
        $rewritten = __jitenv_rewrite_buffer $buffer
        if ($rewritten -ne $buffer) {
            [Microsoft.PowerShell.PSConsoleReadLine]::Replace(0, $buffer.Length, $rewritten)
        }
        [Microsoft.PowerShell.PSConsoleReadLine]::AcceptLine()
    }
}
