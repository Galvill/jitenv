// Command render-docs performs marker-bounded version-pin substitution
// on the static site under web/. It is invoked by the website-update
// release workflow (#253). See package internal/rendocs for the marker
// contract and the >50% safety bail.
//
// Usage:
//
//	render-docs [-docs DIR] [-tag vX.Y.Z] [-dry-run]
//
// With no -tag it resolves the tag from GITHUB_REF / GITHUB_REF_NAME,
// then `gh release view`. Exit status is non-zero on any error (an RC
// tag, a runaway-diff bail, an IO failure) so the workflow fails loud.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/gv/jitenv/internal/rendocs"
)

func main() {
	docs := flag.String("docs", "web", "path to the site directory to render")
	tag := flag.String("tag", "", "release tag to substitute (default: resolve from env / gh)")
	dryRun := flag.Bool("dry-run", false, "compute and print changes without writing files")
	flag.Parse()

	res, err := rendocs.Run(context.Background(), rendocs.Options{
		DocsDir: *docs,
		Tag:     *tag,
		DryRun:  *dryRun,
		Out:     os.Stdout,
		Err:     os.Stderr,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "render-docs:", err)
		os.Exit(1)
	}
	if len(res.Changed) == 0 {
		// Exit 0 with no changes — the workflow's no-op diff check
		// handles whether to commit.
		return
	}
	fmt.Printf("render-docs: rewrote %d file(s) for %s\n", len(res.Changed), res.Tag)
}
