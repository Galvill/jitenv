//go:build windows

package cli

import (
	"fmt"
	"os"
	"strconv"
	"syscall"

	"github.com/gv/jitenv/internal/agent"
	"github.com/gv/jitenv/internal/crypto"
)

// keyFlagName is the platform-appropriate flag name the parent
// SpawnDaemon uses to tell the __agent subprocess where to find the
// master key.
//
// As of security #128 the Windows handoff routes the master-key pipe
// through the child's stdin and signals that via --key-handle=stdin.
// The previous design passed the kernel-handle hex on the command
// line, where it was visible to any same-user process and exposed a
// brief DuplicateHandle race window. The hex-value form is still
// accepted for backward compatibility with externally-managed spawns
// (e.g. test fixtures); the production SpawnDaemon no longer takes
// that path.
const keyFlagName = "key-handle"

// keyFlagDefault is the value the flag is set to when the caller did
// not pass one.
const keyFlagDefault = ""

// readKeyFromFlag dispatches to the chosen channel and returns a
// freshly allocated copy of the master key. Caller zeroes.
func readKeyFromFlag(value string) ([]byte, error) {
	switch value {
	case "":
		return nil, fmt.Errorf("%s required (not passed by parent)", keyFlagName)
	case "stdin":
		buf := make([]byte, crypto.KeyLen)
		if _, err := agent.ReadKeyFromReader(os.Stdin, buf); err != nil {
			return nil, fmt.Errorf("read key from stdin: %w", err)
		}
		// Subsequent reads of stdin should EOF — close it now so any
		// downstream code that opens os.Stdin sees a clean state.
		_ = os.Stdin.Close()
		return buf, nil
	}
	// Legacy hex-handle fallback. Retained for test fixtures that build
	// the spawn pipeline manually; production SpawnDaemon no longer
	// emits this value.
	h, err := strconv.ParseUint(value, 16, 64)
	if err != nil {
		return nil, fmt.Errorf("parse %s=%q: %w", keyFlagName, value, err)
	}
	if h == 0 {
		return nil, fmt.Errorf("%s required (not passed by parent)", keyFlagName)
	}
	return agent.ReadKeyFromHandle(syscall.Handle(h))
}
