$ErrorActionPreference = 'Stop'

# Counterpart to chocolateyInstall.ps1: remove the shims chocolatey
# created for the extracted executables. Uninstall-ChocolateyZipPackage
# reads the extracted-files register that Install-ChocolateyZipPackage
# wrote (keyed by the .zip's leaf name, rendered at pack time) and
# tears down the shims + extracted files. __ZIPNAME__ is the same
# windows-amd64 archive name used in chocolateyInstall.ps1.

$packageName = 'jitenv'

Uninstall-ChocolateyZipPackage -PackageName $packageName -ZipFileName '__ZIPNAME__'
