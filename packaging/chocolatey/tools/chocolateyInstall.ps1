$ErrorActionPreference = 'Stop'

# Downloader install: pull the official windows-amd64 release .zip from
# GitHub and verify its SHA256 before extracting. __URL64__ /
# __CHECKSUM64__ are rendered at pack time by chocolatey.yml from the
# published release + its SHA256SUMS asset. Extracting into $toolsDir
# lets chocolatey's auto-shim put jitenv.exe, jitenv-hook.exe and
# jitenv-tui.exe on PATH (jitenv-hook is the lightweight hot-path binary
# the shell hook spawns; jitenv-tui is the re-exec target for
# `jitenv config`, #182). Whatever .exe files the release zip carries are
# extracted into $toolsDir and shimmed automatically.

$packageName = 'jitenv'
$toolsDir    = Split-Path -Parent $MyInvocation.MyCommand.Definition

$packageArgs = @{
  packageName    = $packageName
  unzipLocation  = $toolsDir
  url64bit       = '__URL64__'
  checksum64     = '__CHECKSUM64__'
  checksumType64 = 'sha256'
}

Install-ChocolateyZipPackage @packageArgs

Write-Host ''
Write-Host 'jitenv installed. Activate the PowerShell hook once:'
Write-Host ''
Write-Host '    jitenv hook install'
Write-Host ''
Write-Host 'Then open a new pwsh tab. The first run of jitenv.exe may trigger'
Write-Host 'SmartScreen (the binary is not yet Authenticode-signed) - run'
Write-Host 'Unblock-File, or right-click > Properties > Unblock, if prompted.'
