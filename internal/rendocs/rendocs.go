// Package rendocs performs mechanical, marker-bounded version-pin
// substitution on the static docs under docs/. It is driven by the
// website-update release workflow (#253 Part B).
//
// Design constraints (from the issue):
//
//   - Marker-based in-place substitution only. No build step, no docs
//     framework. The renderer touches ONLY the bytes between explicit
//     start/end markers; all hand-written narrative content is left
//     byte-for-byte unchanged.
//   - A safety bail: if rendering a file would change more than half of
//     its bytes, that's treated as a marker-handling bug and the render
//     aborts rather than committing garbage.
//
// Supported markers (HTML-comment form for .html, identical form works
// inside .md since GitHub renders raw HTML comments as nothing):
//
//	<!-- VERSION:start -->v0.13.0<!-- VERSION:end -->
//	    → replaced with the full tag, e.g. "v0.14.0".
//
//	<!-- ARTIFACT_VERSION:start -->0.13.0<!-- ARTIFACT_VERSION:end -->
//	    → replaced with the bare version (tag minus a leading "v"),
//	      matching the goreleaser artifact name template
//	      jitenv_{{.Version}}_{{.Os}}_{{.Arch}}.
//
//	<!-- VERSION:asof:start -->...<!-- VERSION:asof:end -->
//	    → a per-page "as of" footer. For .md files the inner text becomes
//	      "_Docs current as of vX.Y.Z._"; for .html it becomes the bare
//	      tag "vX.Y.Z" (the surrounding markup supplies the prose).
package rendocs

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// span describes one marker family: the literal start/end delimiters and
// a function producing the replacement inner text for a given tag.
type span struct {
	start   string
	end     string
	replace func(r Release, ext string) string
}

// Release is the resolved release identity the renderer substitutes.
type Release struct {
	// Tag is the full git tag, e.g. "v0.14.0".
	Tag string
}

// Bare returns the tag without a leading "v": "v0.14.0" → "0.14.0".
// This matches the version segment in goreleaser artifact filenames.
func (r Release) Bare() string {
	return strings.TrimPrefix(r.Tag, "v")
}

// spans is the ordered list of marker families the renderer understands.
// VERSION must be processed before nothing in particular, but the regex
// for each family is anchored to its own delimiters so order is moot.
func spans() []span {
	return []span{
		{
			start:   "<!-- VERSION:start -->",
			end:     "<!-- VERSION:end -->",
			replace: func(r Release, _ string) string { return r.Tag },
		},
		{
			start:   "<!-- ARTIFACT_VERSION:start -->",
			end:     "<!-- ARTIFACT_VERSION:end -->",
			replace: func(r Release, _ string) string { return r.Bare() },
		},
		{
			start: "<!-- VERSION:asof:start -->",
			end:   "<!-- VERSION:asof:end -->",
			replace: func(r Release, ext string) string {
				if ext == ".md" {
					return fmt.Sprintf("_Docs current as of %s._", r.Tag)
				}
				return r.Tag
			},
		},
	}
}

// Render returns the rewritten contents of a single file. ext is the
// file extension (".html" / ".md") and selects the asof phrasing. It
// only ever rewrites bytes between known markers; everything else is
// preserved exactly.
//
// Render is deterministic and pure — no IO — so it is trivially
// golden-testable.
func Render(content string, r Release, ext string) (string, error) {
	out := content
	for _, s := range spans() {
		re, err := regexp.Compile(
			regexp.QuoteMeta(s.start) + "(.*?)" + regexp.QuoteMeta(s.end),
		)
		if err != nil {
			return "", err
		}
		repl := s.start + s.replace(r, ext) + s.end
		out = re.ReplaceAllLiteralString(out, repl)
	}
	return out, nil
}

// TooLargeError is returned when a render would change more bytes than
// the configured ceiling — a signal that marker handling went wrong.
type TooLargeError struct {
	Path        string
	ChangedFrac float64
}

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("rendocs: refusing to rewrite %s: %.0f%% of bytes changed (>50%%); likely a marker bug",
		e.Path, e.ChangedFrac*100)
}

// RenderFile renders one file's content, applies the >50% safety bail,
// and reports whether the content changed. path is used only for the
// extension and for error messages.
func RenderFile(path, content string, r Release) (rendered string, changed bool, err error) {
	ext := strings.ToLower(filepath.Ext(path))
	rendered, err = Render(content, r, ext)
	if err != nil {
		return "", false, err
	}
	if rendered == content {
		return content, false, nil
	}
	if frac := changedFraction(content, rendered); frac > 0.5 {
		return "", false, &TooLargeError{Path: path, ChangedFrac: frac}
	}
	return rendered, true, nil
}

// changedFraction estimates how much of a file changed as the byte
// total of intra-line edits over the original byte total. Marker
// substitution never adds or removes lines, so a positional line
// comparison is exact for the intended edits; within each differing
// line we trim the common prefix/suffix and count only the genuinely
// changed bytes. This stays small for legitimate version-pin edits
// (only the inner span text moves) while still ballooning past 50% if a
// marker bug mangles structure (whole lines diverge or counts change).
func changedFraction(a, b string) float64 {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return 1
	}
	la := strings.Split(a, "\n")
	lb := strings.Split(b, "\n")
	changed := 0
	n := len(la)
	if len(lb) > n {
		n = len(lb)
	}
	for i := 0; i < n; i++ {
		var lineA, lineB string
		if i < len(la) {
			lineA = la[i]
		}
		if i < len(lb) {
			lineB = lb[i]
		}
		if lineA != lineB {
			changed += lineEditBytes(lineA, lineB)
		}
	}
	return float64(changed) / float64(len(a))
}

// lineEditBytes returns the number of original-line bytes outside the
// common prefix/suffix shared with the rewritten line — a tight bound
// on how much of the original line was rewritten.
func lineEditBytes(a, b string) int {
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	if d := len(a) - p - s; d > 0 {
		return d
	}
	return 0
}

// IsStableTag reports whether tag is a stable release tag (no
// prerelease / RC suffix). The website-update workflow is stable-only,
// so RC tags like "v0.14.0-rc.1" must not trigger a docs rewrite. The
// workflow gates on this too, but the renderer enforces it as a
// belt-and-suspenders guard.
func IsStableTag(tag string) bool {
	t := strings.TrimSpace(tag)
	if t == "" {
		return false
	}
	// A SemVer prerelease is anything after the first '-' in the
	// version core. We treat any '-' as prerelease.
	core := strings.TrimPrefix(t, "v")
	return !strings.Contains(core, "-")
}
