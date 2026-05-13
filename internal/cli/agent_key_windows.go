//go:build windows

package cli

import (
	"fmt"
	"strconv"
	"syscall"

	"github.com/gv/jitenv/internal/agent"
)

// keyFlagName is the platform-appropriate flag name the parent
// SpawnDaemon uses to tell the __agent subprocess where to find the
// master key. On Windows there is no fixed fd 3 — the parent passes
// the hex value of a kernel handle inherited via
// SysProcAttr.AdditionalInheritedHandles.
const keyFlagName = "key-handle"

// keyFlagDefault is the value the flag is set to when the caller did
// not pass one. Zero is invalid for an inherited handle, so it doubles
// as a "you forgot the flag" sentinel.
const keyFlagDefault = "0"

// readKeyFromFlag converts the raw --key-handle hex value into a freshly
// allocated copy of the master key. Caller zeroes.
func readKeyFromFlag(value string) ([]byte, error) {
	h, err := strconv.ParseUint(value, 16, 64)
	if err != nil {
		return nil, fmt.Errorf("parse %s=%q: %w", keyFlagName, value, err)
	}
	if h == 0 {
		return nil, fmt.Errorf("%s required (not passed by parent)", keyFlagName)
	}
	return agent.ReadKeyFromHandle(syscall.Handle(h))
}
