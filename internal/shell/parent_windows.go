//go:build windows

package shell

// parentProcessName is a no-op on Windows. DetectShellDetailed pins
// the Windows branch to "powershell" without consulting this; a
// separate follow-up will inspect the parent process to distinguish
// pwsh 7 from Windows PowerShell 5.x and cmd.exe.
func parentProcessName() string { return "" }
