package tui

import (
	"strings"
	"testing"

	"github.com/gv/jitenv/internal/version"
)

// TestRenderFooter_PipeSeparatedSegments guards the global footer
// shape: a single centered line of pipe-separated segments —
// `jitenv | © 2026 Gal Villaret | MIT | <version>`. "jitenv" appears
// only once (in the leading segment), not duplicated as a prefix on
// the version.
func TestRenderFooter_PipeSeparatedSegments(t *testing.T) {
	prev := version.Version
	t.Cleanup(func() { version.Version = prev })
	version.Version = "9.9.9"

	out := renderFooter(120)
	for _, want := range []string{"jitenv", "© 2026 Gal Villaret", "MIT", "9.9.9", " | "} {
		if !strings.Contains(out, want) {
			t.Fatalf("footer missing %q: %q", want, out)
		}
	}
	if strings.Count(out, "jitenv") != 1 {
		t.Fatalf("expected 'jitenv' exactly once, got %d in %q", strings.Count(out, "jitenv"), out)
	}
}

// TestRenderFooter_DropsVersionWhenNarrow falls back to the
// jitenv | copyright | MIT line on tight widths so the footer never
// wraps or clobbers the status line above it. The version is always
// reachable via -v.
func TestRenderFooter_DropsVersionWhenNarrow(t *testing.T) {
	prev := version.Version
	t.Cleanup(func() { version.Version = prev })
	version.Version = "9.9.9"

	out := renderFooter(20)
	if strings.Contains(out, "9.9.9") {
		t.Fatalf("narrow footer should drop version, got %q", out)
	}
}
