// Package builtin imports the first-party sync Adapter implementations
// so that their init() blocks register them with the global sync-adapter
// registry. Mirrors internal/sources/builtin. Blank-imported from
// cmd/jitenv/main.go.
//
// v1 ships the "file" (local / mounted filesystem), "ssh", and "s3"
// adapters. Adding a new adapter is a pure registration call here (a
// blank import of its package) — no core change.
package builtin

import (
	_ "github.com/gv/jitenv/internal/syncadapters/file"
	_ "github.com/gv/jitenv/internal/syncadapters/s3"
	_ "github.com/gv/jitenv/internal/syncadapters/ssh"
)
