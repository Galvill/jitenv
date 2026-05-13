//go:build !windows

package cli

import (
	"fmt"
	"strconv"

	"github.com/gv/jitenv/internal/agent"
)

// keyFlagName is the platform-appropriate flag name the parent
// SpawnDaemon uses to tell the __agent subprocess where to find the
// master key. On Unix it's a fixed fd (3) inherited via ExtraFiles; on
// Windows it's the hex value of a kernel handle inherited via
// AdditionalInheritedHandles.
const keyFlagName = "key-fd"

// keyFlagDefault is the value the flag is set to when the caller did not
// pass one. The Unix path historically defaulted to fd 3 and the e2e
// tests rely on that.
const keyFlagDefault = "3"

// readKeyFromFlag converts the raw --key-fd / --key-handle value into a
// freshly allocated copy of the master key. Caller zeroes.
func readKeyFromFlag(value string) ([]byte, error) {
	fd, err := strconv.Atoi(value)
	if err != nil {
		return nil, fmt.Errorf("parse %s=%q: %w", keyFlagName, value, err)
	}
	return agent.ReadKeyFromFd(fd)
}
