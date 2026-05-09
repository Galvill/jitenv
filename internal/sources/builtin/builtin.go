// Package builtin imports the first-party Source implementations so
// that their init() blocks register them with the global sources registry.
package builtin

import (
	_ "github.com/gv/jitenv/internal/sources/aws"
	_ "github.com/gv/jitenv/internal/sources/local"
	_ "github.com/gv/jitenv/internal/sources/noop"
)
