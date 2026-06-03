// Package builtin imports the first-party sync Adapter implementations
// so that their init() blocks register them with the global sync-adapter
// registry. Mirrors internal/sources/builtin. Blank-imported from
// cmd/jitenv/main.go.
//
// Ships the "file" (local / mounted filesystem), "ssh", and "s3"
// adapters. The S3 adapter stores the encrypted blob as an object in an
// Amazon S3 (or S3-compatible) bucket; adding a new adapter is a pure
// registration call here — no core change.
package builtin

import (
	_ "github.com/gv/jitenv/internal/syncadapters/file"
	_ "github.com/gv/jitenv/internal/syncadapters/s3"
	_ "github.com/gv/jitenv/internal/syncadapters/ssh"
)
