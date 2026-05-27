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

// TestScrollOffset covers the windowing math behind the var-tree
// scroll fix (#194): keep the cursor inside [off, off+visible) with
// minimal movement, clamp to the last page, and treat "everything
// fits" as offset 0.
func TestScrollOffset(t *testing.T) {
	cases := []struct {
		name                        string
		off, cursor, total, visible int
		want                        int
	}{
		{"all fits", 0, 5, 8, 10, 0},
		{"cursor below window scrolls down", 0, 9, 30, 5, 5},
		{"cursor above window scrolls up", 10, 3, 30, 5, 3},
		{"cursor inside window no move", 5, 6, 30, 5, 5},
		{"clamp to last page", 0, 29, 30, 5, 25},
		{"zero visible treated as 1", 0, 10, 30, 0, 10},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := scrollOffset(c.off, c.cursor, c.total, c.visible); got != c.want {
				t.Errorf("scrollOffset(%d,%d,%d,%d) = %d, want %d",
					c.off, c.cursor, c.total, c.visible, got, c.want)
			}
		})
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
