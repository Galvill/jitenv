// Package builtin imports the first-party sync Adapter implementations
// so that their init() blocks register them with the global sync-adapter
// registry. Mirrors internal/sources/builtin. Blank-imported from
// cmd/jitenv/main.go.
//
// v1 ships the "file" (local / mounted filesystem) and "ssh" adapters.
// An "s3" adapter is tracked as a follow-up: the AWS S3 service client
// is not yet vendored, and adding it is a pure registration call here
// once written — no core change.
package builtin

import (
	_ "github.com/gv/jitenv/internal/syncadapters/file"
	_ "github.com/gv/jitenv/internal/syncadapters/ssh"
)
