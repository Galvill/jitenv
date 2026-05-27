// Package gitauth wires git authentication through jitenv: the
// `jitenv clone` subcommand uses it to capture a PAT once and store
// it in an encrypted bag, and the per-user `git-askpass` shim binds
// git's GIT_ASKPASS variable to a small jitenv entrypoint that reads
// the PAT from the per-command env rather than from disk. Issue #179.
package gitauth

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
)

// JitenvGitTokenEnv is the env-var name the cwd_glob mapping injects
// into git's process tree. The __git_askpass subcommand reads it back
// out here.
//
// Naming: `JITENV_*` keeps it inside the jitenv namespace (no clash
// with git's own GIT_* convention) and signals to a curious reader
// that the value came from jitenv, not from a global env.
const JitenvGitTokenEnv = "JITENV_GIT_TOKEN"

// GitUsernameForToken is the placeholder username paired with a
// host-issued PAT under HTTPS Basic auth. Github accepts any non-
// empty value; "oauth2" is the convention every doc out there uses.
const GitUsernameForToken = "oauth2"

// Askpass handles one git askpass invocation. git calls
// `$GIT_ASKPASS "Username for 'https://github.com':"` and reads stdout
// for the answer; we match on the prompt's leading word so the
// behaviour is independent of the host / repo string git appends.
//
// Prompt → response:
//   - "Username..."  → "oauth2"
//   - "Password..."  → contents of $JITENV_GIT_TOKEN
//   - anything else  → empty line (lets git fall through to its own
//     prompt mechanism instead of mis-answering)
//
// Returns nil on success; non-nil only on stdout write failure. Any
// missing env / unknown prompt is silently downgraded to an empty
// answer — git's own retry / interactive-prompt fallback handles it.
func Askpass(prompt string, out io.Writer) error {
	answer := ""
	switch firstWord(prompt) {
	case "Username":
		answer = GitUsernameForToken
	case "Password":
		// Read the token JUST-IN-TIME — never cache. Each askpass
		// invocation is a fresh process; the env is the only
		// transport from the cwd_glob shim's env-injection.
		answer = os.Getenv(JitenvGitTokenEnv)
	}
	_, err := fmt.Fprintln(out, answer)
	return err
}

// firstWord returns the leading whitespace-delimited token of s
// with any trailing colon stripped. "Username for 'https://...':"
// → "Username".
func firstWord(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSuffix(s, ":")
}

// ShimPath returns the per-user filesystem path of the askpass shim
// installed by EnsureShim. It does not check that the file exists.
//
//   - Unix:    $XDG_DATA_HOME/jitenv/bin/git-askpass.sh
//     ($HOME/.local/share/jitenv/bin/git-askpass.sh fallback)
//   - Windows: %LOCALAPPDATA%\jitenv\bin\git-askpass.bat
//
// XDG_DATA_HOME (not _RUNTIME_) because the path is baked into the
// user's encrypted config and has to survive a reboot. The agent's
// runtime dir is volatile.
func ShimPath() (string, error) {
	return shimPath()
}

// EnsureShim returns the absolute path to the askpass shim, creating
// it on disk if it doesn't exist (or rewriting it if jitenv has been
// reinstalled at a new absolute path). The shim is a tiny script
// that execs the running jitenv binary with `__git_askpass`.
//
// jitenvExe must be the absolute path of the jitenv binary at the
// time the shim is created (caller passes os.Executable() typically).
// Baking the absolute path makes the shim independent of $PATH at
// git-invocation time — git's child process inherits a clean env.
func EnsureShim(jitenvExe string) (string, error) {
	path, err := shimPath()
	if err != nil {
		return "", err
	}
	body := shimBody(jitenvExe)
	// Read-compare-write so the common case (already exists, same
	// contents) doesn't touch the file. Avoids stomping the mtime on
	// every clone.
	if existing, err := os.ReadFile(path); err == nil && string(existing) == body {
		return path, nil
	}
	if err := os.MkdirAll(parentDir(path), 0o700); err != nil {
		return "", err
	}
	if err := writeShim(path, body); err != nil {
		return "", err
	}
	return path, nil
}

// shimBody returns the platform-appropriate script body. On Unix
// `exec` replaces the shim shell so the askpass process tree stays
// shallow; on Windows a CMD batch file does a plain call.
func shimBody(jitenvExe string) string {
	if runtime.GOOS == "windows" {
		// CMD: %* passes the prompt through to jitenv. EXIT /B
		// preserves jitenv's exit code so git sees the right thing.
		return "@echo off\r\n\"" + jitenvExe + "\" __git_askpass %*\r\nexit /b %ERRORLEVEL%\r\n"
	}
	// POSIX sh: -e exits on error; exec replaces the shell with
	// jitenv so signals propagate. "$@" preserves argv.
	return "#!/bin/sh\nexec '" + jitenvExe + "' __git_askpass \"$@\"\n"
}

func parentDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[:i]
		}
	}
	return "."
}
